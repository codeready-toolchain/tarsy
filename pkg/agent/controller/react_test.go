package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib" // Register pgx driver for database/sql
	"github.com/stretchr/testify/require"
)

func TestReActController_HappyPath(t *testing.T) {
	// LLM calls: 1) tool call 2) final answer
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: I need to check pods.\nAction: k8s.get_pods\nAction Input: {}"},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Pods look good.\nFinal Answer: Everything is healthy."},
				&agent.UsageChunk{InputTokens: 15, OutputTokens: 25, TotalTokens: 40},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running", IsError: false},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Everything is healthy.", result.FinalAnalysis)
	require.Equal(t, 70, result.TokensUsed.TotalTokens)
	require.Equal(t, 2, llm.callCount)
}

func TestReActController_MultipleIterations(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check pods.\nAction: k8s.get_pods\nAction Input: {}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check logs.\nAction: k8s.get_logs\nAction Input: {\"pod\": \"web-1\"}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Found issue.\nFinal Answer: OOM kill on web-1."},
			}},
		},
	}

	tools := []agent.ToolDefinition{
		{Name: "k8s.get_pods", Description: "Get pods"},
		{Name: "k8s.get_logs", Description: "Get logs"},
	}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "web-1 Running"},
			"k8s.get_logs": {Content: "OOMKilled"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, 3, llm.callCount)
}

func TestReActController_UnknownTool(t *testing.T) {
	// LLM calls unknown tool (bad format), then self-corrects
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check pods.\nAction: bad_tool\nAction Input: {}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Let me use the right tool.\nAction: k8s.get_pods\nAction Input: {}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Pods are fine."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, 3, llm.callCount)
}

func TestReActController_MalformedResponse(t *testing.T) {
	// LLM produces malformed response, then self-corrects
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "I'm not sure what to do..."},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Let me try again.\nFinal Answer: The system is healthy."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{{Name: "k8s.get_pods"}}}
	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
}

func TestReActController_MaxIterationsForceConclusion(t *testing.T) {
	// 5 tool-call responses consumed by the main loop (iterations 0-4)
	// + 1 forced-conclusion response consumed by forceConclusion after the loop.
	var responses []mockLLMResponse
	for i := 0; i < 5; i++ {
		responses = append(responses, mockLLMResponse{
			chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check more.\nAction: k8s.get_pods\nAction Input: {}"},
			},
		})
	}
	// Forced conclusion response (consumed by forceConclusion after the loop ends)
	responses = append(responses, mockLLMResponse{
		chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Thought: Based on what I know.\nFinal Answer: System appears healthy."},
		},
	})

	llm := &mockLLMClient{responses: responses}
	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 5
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Contains(t, result.FinalAnalysis, "System appears healthy")
}

func TestReActController_ConsecutiveTimeouts(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{err: context.DeadlineExceeded},
			{err: context.DeadlineExceeded},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusFailed, result.Status)
	require.NotNil(t, result.Error)
}

func TestReActController_LLMErrorRecovery(t *testing.T) {
	// First call errors, second succeeds
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{err: fmt.Errorf("connection error")},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: The system is fine."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
}

func TestReActController_PrevStageContext(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Using context.\nFinal Answer: Based on previous analysis."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "Previous agent found OOM issues.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Verify prev stage context was included in messages
	require.NotNil(t, llm.lastInput)
	found := false
	for _, msg := range llm.lastInput.Messages {
		if strings.Contains(msg.Content, "Previous agent found OOM issues") {
			found = true
			break
		}
	}
	require.True(t, found, "previous stage context not found in LLM messages")
}

func TestReActController_ToolExecutionError(t *testing.T) {
	// Tool fails on first call, LLM retries with a different approach and succeeds
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check pods.\nAction: k8s.get_pods\nAction Input: {}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Tool failed. Let me try logs.\nAction: k8s.get_logs\nAction Input: {\"pod\": \"web-1\"}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Got the data.\nFinal Answer: Pod web-1 is crashing due to OOM."},
			}},
		},
	}

	tools := []agent.ToolDefinition{
		{Name: "k8s.get_pods", Description: "Get pods"},
		{Name: "k8s.get_logs", Description: "Get logs"},
	}

	callCount := 0
	executor := &mockToolExecutorFunc{
		tools: tools,
		executeFn: func(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
			callCount++
			if call.Name == "k8s.get_pods" {
				return nil, fmt.Errorf("connection refused to cluster API")
			}
			return &agent.ToolResult{Content: "OOMKilled at 14:32"}, nil
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Contains(t, result.FinalAnalysis, "OOM")
	require.Equal(t, 2, callCount, "both tool calls should have been attempted")
}

func TestReActController_ForcedConclusionWithFailedLast(t *testing.T) {
	// 4 tool-call responses consumed by iterations 0-3, then iteration 4 errors.
	// After the loop, forceConclusion sees LastInteractionFailed=true and returns
	// a failed result wrapping the original error in a "max iterations" message.
	var responses []mockLLMResponse
	for i := 0; i < 4; i++ {
		responses = append(responses, mockLLMResponse{
			chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check.\nAction: k8s.get_pods\nAction Input: {}"},
			},
		})
	}
	// 5th iteration: LLM error
	responses = append(responses, mockLLMResponse{
		err: fmt.Errorf("service unavailable"),
	})

	llm := &mockLLMClient{responses: responses}
	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 5
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusFailed, result.Status)
	require.NotNil(t, result.Error)
	// Verify the error indicates iteration exhaustion and propagates the original cause.
	errMsg := result.Error.Error()
	require.Contains(t, errMsg, "max iterations")
	require.Contains(t, errMsg, "service unavailable")
}

func TestReActController_ToolNotInAvailableList(t *testing.T) {
	// LLM calls a tool that passes format validation (has dot) but isn't in the tool list
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check.\nAction: nonexistent.tool\nAction Input: {}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Fixed."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools:   tools,
		results: map[string]*agent.ToolResult{},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
}

func TestReActController_NativeToolDataIgnored(t *testing.T) {
	// When native tool data (code executions, groundings) appears in a ReAct response,
	// the controller should NOT create native tool timeline events. It should complete
	// normally with only standard ReAct events (llm_thinking, tool_call, etc.).
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: The system is healthy."},
				&agent.CodeExecutionChunk{Code: "print(1)", Result: ""},
				&agent.CodeExecutionChunk{Code: "", Result: "1"},
				&agent.GroundingChunk{
					WebSearchQueries: []string{"k8s health"},
					Sources:          []agent.GroundingSource{{URI: "https://k8s.io", Title: "K8s"}},
				},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "The system is healthy.", result.FinalAnalysis)

	// Verify no native tool events were created
	events, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	for _, ev := range events {
		require.NotEqual(t, timelineevent.EventTypeCodeExecution, ev.EventType,
			"ReAct should not create code_execution events")
		require.NotEqual(t, timelineevent.EventTypeGoogleSearchResult, ev.EventType,
			"ReAct should not create google_search_result events")
		require.NotEqual(t, timelineevent.EventTypeURLContextResult, ev.EventType,
			"ReAct should not create url_context_result events")
	}
}

func TestReActController_PromptBuilderIntegration(t *testing.T) {
	// Verify the prompt builder produces the expected message structure
	// in the ReAct controller: system msg with three-tier instructions + ReAct format,
	// user msg with tools, alert data, runbook, and analysis task.
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: All clear."},
			}},
		},
	}

	tools := []agent.ToolDefinition{
		{Name: "k8s.get_pods", Description: "List pods", ParametersSchema: `{"properties":{"ns":{"type":"string","description":"Namespace"}},"required":["ns"]}`},
	}
	executor := &mockToolExecutor{tools: tools, results: map[string]*agent.ToolResult{}}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.AlertType = "kubernetes"
	execCtx.RunbookContent = "# Test Runbook\nStep 1: Check pods"
	execCtx.Config.CustomInstructions = "Custom agent instructions for test."
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "Previous agent found high CPU.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Inspect messages sent to LLM
	require.NotNil(t, llm.lastInput)
	require.GreaterOrEqual(t, len(llm.lastInput.Messages), 2)

	systemMsg := llm.lastInput.Messages[0]
	userMsg := llm.lastInput.Messages[1]

	// System message should have: Tier 1 (SRE instructions), ReAct format, task focus
	require.Equal(t, "system", systemMsg.Role)
	require.Contains(t, systemMsg.Content, "General SRE Agent Instructions")
	require.Contains(t, systemMsg.Content, "ReAct")
	require.Contains(t, systemMsg.Content, "Thought:")
	require.Contains(t, systemMsg.Content, "Action:")
	require.Contains(t, systemMsg.Content, "Final Answer:")
	require.Contains(t, systemMsg.Content, "Focus on investigation")

	// Custom instructions (Tier 3) in system
	require.Contains(t, systemMsg.Content, "Custom agent instructions for test.")

	// User message should have: tool descriptions, alert data, runbook, chain context, task
	require.Equal(t, "user", userMsg.Role)
	require.Contains(t, userMsg.Content, "Available tools")
	require.Contains(t, userMsg.Content, "k8s.get_pods")
	require.Contains(t, userMsg.Content, "ns (required, string): Namespace")
	require.Contains(t, userMsg.Content, "Alert Details")
	require.Contains(t, userMsg.Content, "CPU high on prod-server-1") // from execCtx.AlertData
	require.Contains(t, userMsg.Content, "Alert Type")
	require.Contains(t, userMsg.Content, "Runbook Content")
	require.Contains(t, userMsg.Content, "Test Runbook")
	require.Contains(t, userMsg.Content, "Previous Stage Data")
	require.Contains(t, userMsg.Content, "Previous agent found high CPU.")
	require.Contains(t, userMsg.Content, "Your Task")

	// ReAct should NOT pass tools natively — they're described in text
	require.Nil(t, llm.lastInput.Tools)
}

func TestReActController_ForcedConclusionUsesReActFormat(t *testing.T) {
	// Verify the forced conclusion prompt specifically uses the ReAct format
	// (requires "Final Answer:" marker)
	var responses []mockLLMResponse
	for i := 0; i < 3; i++ {
		responses = append(responses, mockLLMResponse{
			chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: More investigation.\nAction: k8s.get_pods\nAction Input: {}"},
			},
		})
	}
	// Forced conclusion response
	responses = append(responses, mockLLMResponse{
		chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Thought: Concluded.\nFinal Answer: System healthy."},
		},
	})

	llm := &mockLLMClient{responses: responses}
	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools:   tools,
		results: map[string]*agent.ToolResult{"k8s.get_pods": {Content: "pod-1 Running"}},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 3
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// The forced conclusion call's messages should contain ReAct-specific format instructions
	require.NotNil(t, llm.lastInput)
	lastUserMsg := ""
	for i := len(llm.lastInput.Messages) - 1; i >= 0; i-- {
		if llm.lastInput.Messages[i].Role == "user" {
			lastUserMsg = llm.lastInput.Messages[i].Content
			break
		}
	}
	require.Contains(t, lastUserMsg, "iteration limit")
	require.Contains(t, lastUserMsg, "Final Answer:")
	require.Contains(t, lastUserMsg, "CRITICAL")
}

// --- Test helpers / mocks ---

// mockLLMResponse defines a single LLM call result.
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
}

func (m *mockLLMClient) Generate(_ context.Context, input *agent.GenerateInput) (<-chan agent.Chunk, error) {
	idx := m.callCount
	m.callCount++
	m.lastInput = input

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
// Defaults: MaxIterations=20, IterationTimeout=120s.
// Tests that need different limits should override execCtx.Config.MaxIterations.
// Note: ChatContext is left zero-valued — controllers don't rely on it.
func newTestExecCtx(t *testing.T, llm agent.LLMClient, toolExec agent.ToolExecutor) *agent.ExecutionContext {
	t.Helper()

	entClient, _ := util.SetupTestDatabase(t)
	svc := newTestServiceBundle(t, entClient)

	// Create the required session, stage, and execution in DB
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
		SetIterationStrategy("react").
		SetStatus(agentexecution.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	// Create a real PromptBuilder with a test MCP registry
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
			IterationStrategy:  config.IterationStrategyReact,
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
