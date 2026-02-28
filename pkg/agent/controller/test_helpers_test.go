package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

type mockLLMResponse struct {
	chunks []agent.Chunk
	err    error
}

// mockLLMClient is a test mock for agent.LLMClient.
// NOTE: Not safe for concurrent use — callCount and lastInput are mutated
// without synchronization. This is fine as long as controllers call Generate
// sequentially (which they currently do).
type mockLLMClient struct {
	responses []mockLLMResponse
	callCount int
	lastInput *agent.GenerateInput

	// capture enables recording all inputs across calls (not just the last one).
	capture        bool
	capturedInputs []*agent.GenerateInput

	// onGenerate is called before processing the response, allowing tests to
	// perform side-effects (e.g. cancel a context) at call time.
	onGenerate func(callIndex int)
}

func (m *mockLLMClient) Generate(_ context.Context, input *agent.GenerateInput) (<-chan agent.Chunk, error) {
	idx := m.callCount
	m.callCount++
	m.lastInput = input
	if m.capture {
		m.capturedInputs = append(m.capturedInputs, input)
	}
	if m.onGenerate != nil {
		m.onGenerate(idx)
	}

	if idx >= len(m.responses) {
		return nil, fmt.Errorf("no more mock responses (call %d)", idx+1)
	}

	r := m.responses[idx]
	if r.err != nil {
		return nil, r.err
	}

	ch := make(chan agent.Chunk, len(r.chunks))
	for _, c := range r.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (m *mockLLMClient) Close() error { return nil }

// mockToolExecutor is a test mock for agent.ToolExecutor.
type mockToolExecutor struct {
	tools   []agent.ToolDefinition
	results map[string]*agent.ToolResult
}

func (m *mockToolExecutor) Execute(_ context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	result, ok := m.results[call.Name]
	if !ok {
		return nil, fmt.Errorf("unexpected tool call: %s", call.Name)
	}
	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: result.Content,
		IsError: result.IsError,
	}, nil
}

func (m *mockToolExecutor) ListTools(_ context.Context) ([]agent.ToolDefinition, error) {
	return m.tools, nil
}

func (m *mockToolExecutor) Close() error { return nil }

// mockToolExecutorFunc is a flexible test mock that allows custom execute functions.
type mockToolExecutorFunc struct {
	tools     []agent.ToolDefinition
	executeFn func(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error)
}

func (m *mockToolExecutorFunc) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	return m.executeFn(ctx, call)
}

func (m *mockToolExecutorFunc) ListTools(_ context.Context) ([]agent.ToolDefinition, error) {
	return m.tools, nil
}

func (m *mockToolExecutorFunc) Close() error { return nil }

// newTestExecCtx creates a test ExecutionContext backed by a real test database.
// Defaults: MaxIterations=20, IterationTimeout=120s, LLMBackend=langchain.
// Tests that need different limits should override execCtx.Config.MaxIterations.
func newTestExecCtx(t *testing.T, llm agent.LLMClient, toolExec agent.ToolExecutor) *agent.ExecutionContext {
	t.Helper()

	entClient, _ := util.SetupTestDatabase(t)
	svc := newTestServiceBundle(t, entClient)

	ctx := context.Background()

	sessionID := uuid.New().String()
	_, err := entClient.AlertSession.Create().
		SetID(sessionID).
		SetAlertData("Test alert: CPU high on prod-server-1").
		SetAgentType("test-agent").
		SetAlertType("test-alert").
		SetChainID("test-chain").
		SetStatus(alertsession.StatusInProgress).
		SetAuthor("test").
		Save(ctx)
	require.NoError(t, err)

	stageID := uuid.New().String()
	_, err = entClient.Stage.Create().
		SetID(stageID).
		SetSessionID(sessionID).
		SetStageName("test-stage").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStatus(stage.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	execID := uuid.New().String()
	_, err = entClient.AgentExecution.Create().
		SetID(execID).
		SetSessionID(sessionID).
		SetStageID(stageID).
		SetAgentName("test-agent").
		SetAgentIndex(1).
		SetLlmBackend("langchain").
		SetStatus(agentexecution.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	testRegistry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{})
	pb := prompt.NewPromptBuilder(testRegistry)

	return &agent.ExecutionContext{
		SessionID:   sessionID,
		StageID:     stageID,
		ExecutionID: execID,
		AgentName:   "test-agent",
		AgentIndex:  1,
		AlertData:   "Test alert: CPU high on prod-server-1",
		AlertType:   "test-alert",
		Config: &agent.ResolvedAgentConfig{
			AgentName:          "test-agent",
			Type:               config.AgentTypeDefault,
			LLMBackend:         config.LLMBackendLangChain,
			LLMProvider:        &config.LLMProviderConfig{Model: "test-model"},
			MaxIterations:      20,
			IterationTimeout:   120 * time.Second,
			CustomInstructions: "You are a test agent.",
		},
		LLMClient:     llm,
		ToolExecutor:  toolExec,
		PromptBuilder: pb,
		Services:      svc,
	}
}

// mockSubAgentCollector implements agent.SubAgentResultCollector for testing.
type mockSubAgentCollector struct {
	// Results to return from TryDrainResult (consumed in order).
	drainResults []agent.ConversationMessage
	drainIdx     int

	// Results to return from WaitForResult (consumed in order).
	waitResults []agent.ConversationMessage
	waitErrors  []error
	waitIdx     int

	pending bool
}

func (m *mockSubAgentCollector) TryDrainResult() (agent.ConversationMessage, bool) {
	if m.drainIdx >= len(m.drainResults) {
		return agent.ConversationMessage{}, false
	}
	msg := m.drainResults[m.drainIdx]
	m.drainIdx++
	return msg, true
}

func (m *mockSubAgentCollector) WaitForResult(ctx context.Context) (agent.ConversationMessage, error) {
	if m.waitIdx >= len(m.waitResults) {
		<-ctx.Done()
		return agent.ConversationMessage{}, ctx.Err()
	}
	msg := m.waitResults[m.waitIdx]
	var err error
	if m.waitIdx < len(m.waitErrors) {
		err = m.waitErrors[m.waitIdx]
	}
	m.waitIdx++
	if err != nil {
		return agent.ConversationMessage{}, err
	}
	// After delivering a wait result, mark as no longer pending
	m.pending = false
	return msg, nil
}

func (m *mockSubAgentCollector) HasPending() bool {
	return m.pending
}

func newTestServiceBundle(t *testing.T, entClient *ent.Client) *agent.ServiceBundle {
	t.Helper()
	msgSvc := services.NewMessageService(entClient)
	return &agent.ServiceBundle{
		Timeline:    services.NewTimelineService(entClient),
		Message:     msgSvc,
		Interaction: services.NewInteractionService(entClient, msgSvc),
		Stage:       services.NewStageService(entClient),
	}
}
