package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	agentctx "github.com/codeready-toolchain/tarsy/pkg/agent/context"
	"github.com/codeready-toolchain/tarsy/pkg/agent/orchestrator"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// ────────────────────────────────────────────────────────────
// Tool executor
// ────────────────────────────────────────────────────────────

// createToolExecutor creates an MCP tool executor or falls back to a stub.
// Package-level function shared by RealSessionExecutor and ChatMessageExecutor.
func createToolExecutor(
	ctx context.Context,
	mcpFactory *mcp.ClientFactory,
	serverIDs []string,
	toolFilter map[string][]string,
	logger *slog.Logger,
) (agent.ToolExecutor, map[string]string) {
	if mcpFactory != nil && len(serverIDs) > 0 {
		mcpExecutor, mcpClient, mcpErr := mcpFactory.CreateToolExecutor(ctx, serverIDs, toolFilter)
		if mcpErr != nil {
			logger.Warn("Failed to create MCP tool executor, using stub", "error", mcpErr)
			return agent.NewStubToolExecutor(nil), nil
		}
		var failedServers map[string]string
		if mcpClient != nil {
			failedServers = mcpClient.FailedServers()
		}
		return mcpExecutor, failedServers
	}
	return agent.NewStubToolExecutor(nil), nil
}

// ────────────────────────────────────────────────────────────
// Cancellation / context helpers
// ────────────────────────────────────────────────────────────

// mapCancellation checks if the context was cancelled or timed out and returns
// an appropriate ExecutionResult, or nil if the context is still active.
func (e *RealSessionExecutor) mapCancellation(ctx context.Context) *ExecutionResult {
	if ctx.Err() == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return &ExecutionResult{
			Status: alertsession.StatusTimedOut,
			Error:  fmt.Errorf("session timed out"),
		}
	}
	return &ExecutionResult{
		Status: alertsession.StatusCancelled,
		Error:  context.Canceled,
	}
}

// ────────────────────────────────────────────────────────────
// Stage context
// ────────────────────────────────────────────────────────────

// buildStageContext converts completed stageResults into a context string
// for the next stage's agent prompt.
func (e *RealSessionExecutor) buildStageContext(stages []stageResult) string {
	results := make([]agentctx.StageResult, len(stages))
	for i, s := range stages {
		results[i] = agentctx.StageResult{
			StageName:     s.stageName,
			FinalAnalysis: s.finalAnalysis,
		}
	}
	return agentctx.BuildStageContext(results)
}

// extractFinalAnalysis returns the final analysis from the last completed stage.
// Searches in reverse to find the most recent stage with a non-empty analysis.
func extractFinalAnalysis(stages []stageResult) string {
	for i := len(stages) - 1; i >= 0; i-- {
		if stages[i].finalAnalysis != "" {
			return stages[i].finalAnalysis
		}
	}
	return ""
}

// ────────────────────────────────────────────────────────────
// Progress publishing
// ────────────────────────────────────────────────────────────

// updateSessionProgress updates current_stage_index and current_stage_id on the session.
// Non-blocking: logs warning on failure.
func (e *RealSessionExecutor) updateSessionProgress(ctx context.Context, sessionID string, stageIndex int, stageID string) {
	update := e.dbClient.AlertSession.UpdateOneID(sessionID).
		SetCurrentStageIndex(stageIndex + 1) // 1-based in DB

	if stageID != "" {
		update = update.SetCurrentStageID(stageID)
	}

	if err := update.Exec(ctx); err != nil {
		slog.Warn("Failed to update session progress",
			"session_id", sessionID,
			"stage_index", stageIndex,
			"stage_id", stageID,
			"error", err,
		)
	}
}

// publishSessionProgress publishes a session.progress transient event to the global channel.
// Nil-safe for EventPublisher. Best-effort: logs on failure, never aborts.
func publishSessionProgress(ctx context.Context, eventPublisher agent.EventPublisher, sessionID, stageName string, stageIndex, totalStages, activeExecutions int, statusText string) {
	if eventPublisher == nil {
		return
	}
	// 1-based index for clients, clamped so it never exceeds TotalStages.
	currentIndex := stageIndex + 1
	if totalStages > 0 && currentIndex > totalStages {
		currentIndex = totalStages
	}
	if err := eventPublisher.PublishSessionProgress(ctx, events.SessionProgressPayload{
		BasePayload: events.BasePayload{
			Type:      events.EventTypeSessionProgress,
			SessionID: sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		CurrentStageName:  stageName,
		CurrentStageIndex: currentIndex,
		TotalStages:       totalStages,
		ActiveExecutions:  activeExecutions,
		StatusText:        statusText,
	}); err != nil {
		slog.Warn("Failed to publish session progress",
			"session_id", sessionID,
			"stage_name", stageName,
			"error", err,
		)
	}
}

// publishExecutionProgress publishes an execution.progress transient event.
// Nil-safe for EventPublisher. Best-effort: logs on failure, never aborts.
func publishExecutionProgressFromExecutor(ctx context.Context, eventPublisher agent.EventPublisher, sessionID, stageID, executionID, phase, message string) {
	if eventPublisher == nil {
		return
	}
	if err := eventPublisher.PublishExecutionProgress(ctx, sessionID, events.ExecutionProgressPayload{
		BasePayload: events.BasePayload{
			Type:      events.EventTypeExecutionProgress,
			SessionID: sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		StageID:     stageID,
		ExecutionID: executionID,
		Phase:       phase,
		Message:     message,
	}); err != nil {
		slog.Warn("Failed to publish execution progress",
			"session_id", sessionID,
			"phase", phase,
			"error", err,
		)
	}
}

// publishExecutionStatus publishes an execution.status transient event.
// Nil-safe for EventPublisher. Best-effort: logs on failure, never aborts.
// agentIndex is 1-based and preserves the chain config ordering.
func publishExecutionStatus(ctx context.Context, eventPublisher agent.EventPublisher, sessionID, stageID, executionID string, agentIndex int, status, errMsg string) {
	if eventPublisher == nil {
		return
	}
	if err := eventPublisher.PublishExecutionStatus(ctx, sessionID, events.ExecutionStatusPayload{
		BasePayload: events.BasePayload{
			Type:      events.EventTypeExecutionStatus,
			SessionID: sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		StageID:      stageID,
		ExecutionID:  executionID,
		AgentIndex:   agentIndex,
		Status:       status,
		ErrorMessage: errMsg,
	}); err != nil {
		slog.Warn("Failed to publish execution status",
			"session_id", sessionID,
			"execution_id", executionID,
			"status", status,
			"error", err,
		)
	}
}

// publishStageStatus publishes a stage.status event. Nil-safe for EventPublisher.
// Package-level function shared by RealSessionExecutor and ChatMessageExecutor.
func publishStageStatus(ctx context.Context, eventPublisher agent.EventPublisher, sessionID, stageID, stageName string, stageIndex int, status string) {
	if eventPublisher == nil {
		return
	}
	if err := eventPublisher.PublishStageStatus(ctx, sessionID, events.StageStatusPayload{
		BasePayload: events.BasePayload{
			Type:      events.EventTypeStageStatus,
			SessionID: sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		StageID:    stageID,
		StageName:  stageName,
		StageIndex: stageIndex + 1, // 1-based for clients
		Status:     status,
	}); err != nil {
		slog.Warn("Failed to publish stage status",
			"session_id", sessionID,
			"stage_name", stageName,
			"status", status,
			"error", err,
		)
	}
}

// ────────────────────────────────────────────────────────────
// MCP selection resolution
// ────────────────────────────────────────────────────────────

// resolveMCPSelection determines the MCP servers and tool filter for this session.
// If the session has an MCP override (mcp_selection JSON), it replaces the chain
// config entirely (replace semantics, not merge).
//
// Side effects when the override includes NativeTools:
//  1. Sets resolvedConfig.NativeToolsOverride (used by recordLLMInteraction metadata).
//  2. Clones resolvedConfig.LLMProvider and merges the override into the clone's
//     NativeTools map, so the gRPC layer (toProtoLLMConfig) sends the correct
//     config to the Python LLM service.
//
// Package-level function shared by RealSessionExecutor and ChatMessageExecutor.
// Returns (serverIDs, toolFilter, error).
func resolveMCPSelection(
	session *ent.AlertSession,
	resolvedConfig *agent.ResolvedAgentConfig,
	mcpRegistry *config.MCPServerRegistry,
) ([]string, map[string][]string, error) {
	// No override — use chain config (existing behavior)
	if len(session.McpSelection) == 0 {
		return resolvedConfig.MCPServers, nil, nil
	}

	// Deserialize override
	override, err := models.ParseMCPSelectionConfig(session.McpSelection)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse MCP selection: %w", err)
	}
	if override == nil {
		// ParseMCPSelectionConfig returns nil for empty maps
		return resolvedConfig.MCPServers, nil, nil
	}

	// Build serverIDs and toolFilter from override
	serverIDs := make([]string, 0, len(override.Servers))
	toolFilter := make(map[string][]string)

	for _, sel := range override.Servers {
		// Validate server exists in registry
		if mcpRegistry != nil && !mcpRegistry.Has(sel.Name) {
			return nil, nil, fmt.Errorf("MCP server %q from override not found in configuration", sel.Name)
		}
		serverIDs = append(serverIDs, sel.Name)

		// Only add to toolFilter if specific tools are requested
		if len(sel.Tools) > 0 {
			toolFilter[sel.Name] = sel.Tools
		}
	}

	// Return nil toolFilter if no server has tool restrictions
	if len(toolFilter) == 0 {
		toolFilter = nil
	}

	// Apply native tools override to the resolved config.
	// Store the override struct for recordLLMInteraction metadata, and merge
	// individual overrides into a cloned LLMProvider so the gRPC call path
	// sends the correct native tools to the Python LLM service.
	if override.NativeTools != nil {
		resolvedConfig.NativeToolsOverride = override.NativeTools
		applyNativeToolsOverride(resolvedConfig, override.NativeTools)
	}

	return serverIDs, toolFilter, nil
}

// applyNativeToolsOverride clones resolvedConfig.LLMProvider and merges the
// per-alert native tools override into the clone's NativeTools map.
// The clone avoids mutating the shared config-registry pointer.
func applyNativeToolsOverride(resolvedConfig *agent.ResolvedAgentConfig, nt *models.NativeToolsConfig) {
	orig := resolvedConfig.LLMProvider
	if orig == nil {
		return
	}

	// Shallow copy the provider config; only NativeTools map differs.
	cloned := *orig
	cloned.NativeTools = make(map[config.GoogleNativeTool]bool, len(orig.NativeTools))
	for k, v := range orig.NativeTools {
		cloned.NativeTools[k] = v
	}

	if nt.GoogleSearch != nil {
		cloned.NativeTools[config.GoogleNativeToolGoogleSearch] = *nt.GoogleSearch
	}
	if nt.CodeExecution != nil {
		cloned.NativeTools[config.GoogleNativeToolCodeExecution] = *nt.CodeExecution
	}
	if nt.URLContext != nil {
		cloned.NativeTools[config.GoogleNativeToolURLContext] = *nt.URLContext
	}

	resolvedConfig.LLMProvider = &cloned
}

// countExpectedStages computes the total number of progress steps for the chain,
// including synthesis stages (for multi-agent/replica stages) and the executive
// summary step. Used for accurate progress reporting so CurrentStageIndex never
// exceeds TotalStages.
func countExpectedStages(chain *config.ChainConfig) int {
	total := len(chain.Stages)
	for _, stageCfg := range chain.Stages {
		if len(stageCfg.Agents) > 1 || stageCfg.Replicas > 1 {
			total++ // synthesis stage will follow
		}
	}
	total++ // executive summary step
	return total
}

// ────────────────────────────────────────────────────────────
// Config builders
// ────────────────────────────────────────────────────────────

// buildConfigs creates execution configs for a stage. For single-agent stages,
// returns a single config. Same path, no branching.
func buildConfigs(stageCfg config.StageConfig) []executionConfig {
	if stageCfg.Replicas > 1 {
		return buildReplicaConfigs(stageCfg)
	}
	return buildMultiAgentConfigs(stageCfg)
}

// buildMultiAgentConfigs creates one executionConfig per agent in the stage.
// For single-agent stages, returns []executionConfig with 1 entry.
func buildMultiAgentConfigs(stageCfg config.StageConfig) []executionConfig {
	configs := make([]executionConfig, len(stageCfg.Agents))
	for i, agentCfg := range stageCfg.Agents {
		configs[i] = executionConfig{
			agentConfig: agentCfg,
			displayName: agentCfg.Name,
		}
	}
	return configs
}

// buildReplicaConfigs replicates the first agent config N times.
// Display names: {BaseName}-1, {BaseName}-2, etc.
func buildReplicaConfigs(stageCfg config.StageConfig) []executionConfig {
	baseAgent := stageCfg.Agents[0]
	configs := make([]executionConfig, stageCfg.Replicas)
	for i := 0; i < stageCfg.Replicas; i++ {
		configs[i] = executionConfig{
			agentConfig: baseAgent,
			displayName: fmt.Sprintf("%s-%d", baseAgent.Name, i+1),
		}
	}
	return configs
}

// ────────────────────────────────────────────────────────────
// Policy resolution
// ────────────────────────────────────────────────────────────

// resolvedSuccessPolicy resolves the success policy for a stage:
// stage config > system default > fallback SuccessPolicyAny.
func (e *RealSessionExecutor) resolvedSuccessPolicy(input executeStageInput) config.SuccessPolicy {
	if input.stageConfig.SuccessPolicy != "" {
		return input.stageConfig.SuccessPolicy
	}
	if e.cfg.Defaults != nil && e.cfg.Defaults.SuccessPolicy != "" {
		return e.cfg.Defaults.SuccessPolicy
	}
	return config.SuccessPolicyAny
}

// ────────────────────────────────────────────────────────────
// Orchestrator resolution
// ────────────────────────────────────────────────────────────

// resolveOrchestratorGuardrails merges hardcoded fallbacks < defaults.orchestrator < per-agent orchestrator config.
// The config validator (pkg/config/validator.go) rejects invalid values at load
// time, but we clamp here as defense-in-depth for programmatic construction.
func resolveOrchestratorGuardrails(cfg *config.Config, agentDef *config.AgentConfig) *orchestrator.OrchestratorGuardrails {
	const (
		defaultMaxConcurrent = 5
		defaultAgentTimeout  = 300 * time.Second
		defaultMaxBudget     = 600 * time.Second
	)

	g := &orchestrator.OrchestratorGuardrails{
		MaxConcurrentAgents: defaultMaxConcurrent,
		AgentTimeout:        defaultAgentTimeout,
		MaxBudget:           defaultMaxBudget,
	}
	if cfg.Defaults != nil && cfg.Defaults.Orchestrator != nil {
		applyOrchestratorConfig(g, cfg.Defaults.Orchestrator)
	}
	if agentDef.Orchestrator != nil {
		applyOrchestratorConfig(g, agentDef.Orchestrator)
	}

	if g.MaxConcurrentAgents < 1 {
		g.MaxConcurrentAgents = defaultMaxConcurrent
	}
	if g.AgentTimeout <= 0 {
		g.AgentTimeout = defaultAgentTimeout
	}
	if g.MaxBudget <= 0 {
		g.MaxBudget = defaultMaxBudget
	}
	return g
}

func applyOrchestratorConfig(g *orchestrator.OrchestratorGuardrails, oc *config.OrchestratorConfig) {
	if oc.MaxConcurrentAgents != nil {
		g.MaxConcurrentAgents = *oc.MaxConcurrentAgents
	}
	if oc.AgentTimeout != nil {
		g.AgentTimeout = *oc.AgentTimeout
	}
	if oc.MaxBudget != nil {
		g.MaxBudget = *oc.MaxBudget
	}
}

// resolveSubAgents returns the sub_agents override from the most specific level
// in the hierarchy: stage-agent > stage > chain. Returns nil if no override
// (meaning the full global registry is used).
func resolveSubAgents(chain *config.ChainConfig, stage config.StageConfig, agentCfg config.StageAgentConfig) []string {
	if len(agentCfg.SubAgents) > 0 {
		return agentCfg.SubAgents
	}
	if len(stage.SubAgents) > 0 {
		return stage.SubAgents
	}
	if len(chain.SubAgents) > 0 {
		return chain.SubAgents
	}
	return nil
}

// ────────────────────────────────────────────────────────────
// DB helpers
// ────────────────────────────────────────────────────────────

// parallelTypePtr returns the parallel_type for DB storage, or nil for single-agent stages.
func parallelTypePtr(stageCfg config.StageConfig) *string {
	if stageCfg.Replicas > 1 {
		s := "replica"
		return &s
	}
	if len(stageCfg.Agents) > 1 {
		s := "multi_agent"
		return &s
	}
	return nil
}

// successPolicyPtr returns the resolved success policy as *string for DB storage,
// or nil for single-agent stages (policy is irrelevant when there's only one agent).
func successPolicyPtr(stageCfg config.StageConfig, resolved config.SuccessPolicy) *string {
	if len(stageCfg.Agents) <= 1 && stageCfg.Replicas <= 1 {
		return nil
	}
	s := string(resolved)
	return &s
}

// ────────────────────────────────────────────────────────────
// Result aggregation
// ────────────────────────────────────────────────────────────

// collectAndSort drains the indexedAgentResult channel and returns results
// sorted by their original launch index.
func collectAndSort(ch <-chan indexedAgentResult) []agentResult {
	var indexed []indexedAgentResult
	for iar := range ch {
		indexed = append(indexed, iar)
	}
	sort.Slice(indexed, func(i, j int) bool {
		return indexed[i].index < indexed[j].index
	})
	results := make([]agentResult, len(indexed))
	for i, iar := range indexed {
		results[i] = iar.result
	}
	return results
}

// aggregateStatus determines the overall stage status from agent results and
// the resolved success policy. Works identically for 1 or N agents.
func aggregateStatus(results []agentResult, policy config.SuccessPolicy) alertsession.Status {
	var completed, failed, timedOut, cancelled int

	for _, r := range results {
		switch mapAgentStatusToSessionStatus(r.status) {
		case alertsession.StatusCompleted:
			completed++
		case alertsession.StatusTimedOut:
			timedOut++
		case alertsession.StatusCancelled:
			cancelled++
		default:
			failed++
		}
	}

	nonSuccess := failed + timedOut + cancelled

	switch policy {
	case config.SuccessPolicyAll:
		if nonSuccess == 0 {
			return alertsession.StatusCompleted
		}
	default: // SuccessPolicyAny (default when unset)
		if completed > 0 {
			return alertsession.StatusCompleted
		}
	}

	// Stage failed — use most specific terminal status when uniform
	if nonSuccess == timedOut {
		return alertsession.StatusTimedOut
	}
	if nonSuccess == cancelled {
		return alertsession.StatusCancelled
	}
	return alertsession.StatusFailed
}

// aggregateError builds a descriptive error for failed stages.
// Single-agent: returns the agent's error directly.
// Multi-agent: lists each non-successful agent with details.
func aggregateError(results []agentResult, stageStatus alertsession.Status, stageCfg config.StageConfig) error {
	if stageStatus == alertsession.StatusCompleted {
		return nil
	}

	// Single agent — passthrough
	if len(results) == 1 {
		return results[0].err
	}

	// Multi-agent — build descriptive error
	var nonSuccess int
	for _, r := range results {
		if mapAgentStatusToSessionStatus(r.status) != alertsession.StatusCompleted {
			nonSuccess++
		}
	}

	policy := "any"
	if stageCfg.SuccessPolicy != "" {
		policy = string(stageCfg.SuccessPolicy)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "multi-agent stage failed: %d/%d executions failed (policy: %s)", nonSuccess, len(results), policy)

	sb.WriteString("\n\nFailed agents:")
	for i, r := range results {
		sessionStatus := mapAgentStatusToSessionStatus(r.status)
		if sessionStatus == alertsession.StatusCompleted {
			continue
		}
		errMsg := "unknown error"
		if r.err != nil {
			errMsg = r.err.Error()
		}
		fmt.Fprintf(&sb, "\n  - agent %d (%s): %s", i+1, sessionStatus, errMsg)
	}

	return fmt.Errorf("%s", sb.String())
}

// mapTerminalStatus extracts a terminal status string for event publishing.
func mapTerminalStatus(sr stageResult) string {
	switch sr.status {
	case alertsession.StatusCompleted:
		return events.StageStatusCompleted
	case alertsession.StatusFailed:
		return events.StageStatusFailed
	case alertsession.StatusTimedOut:
		return events.StageStatusTimedOut
	case alertsession.StatusCancelled:
		return events.StageStatusCancelled
	default:
		return events.StageStatusFailed
	}
}

// ────────────────────────────────────────────────────────────
// Status mappers
// ────────────────────────────────────────────────────────────

// mapAgentStatusToEntStatus converts agent.ExecutionStatus to ent agentexecution.Status.
// Pending/Active statuses fall through to Failed intentionally — they should
// never reach this mapper since BaseAgent always sets a terminal status before
// returning. Mapping Active to Failed (rather than Active) prevents leaving
// AgentExecution records in a non-terminal state permanently.
func mapAgentStatusToEntStatus(status agent.ExecutionStatus) agentexecution.Status {
	switch status {
	case agent.ExecutionStatusCompleted:
		return agentexecution.StatusCompleted
	case agent.ExecutionStatusFailed:
		return agentexecution.StatusFailed
	case agent.ExecutionStatusTimedOut:
		return agentexecution.StatusTimedOut
	case agent.ExecutionStatusCancelled:
		return agentexecution.StatusCancelled
	default:
		return agentexecution.StatusFailed
	}
}

// mapAgentStatusToSessionStatus converts agent.ExecutionStatus to alertsession.Status.
// Pending/Active statuses fall through to Failed intentionally — they should never
// reach this mapper since BaseAgent always sets a terminal status before returning.
func mapAgentStatusToSessionStatus(status agent.ExecutionStatus) alertsession.Status {
	switch status {
	case agent.ExecutionStatusCompleted:
		return alertsession.StatusCompleted
	case agent.ExecutionStatusFailed:
		return alertsession.StatusFailed
	case agent.ExecutionStatusTimedOut:
		return alertsession.StatusTimedOut
	case agent.ExecutionStatusCancelled:
		return alertsession.StatusCancelled
	default:
		return alertsession.StatusFailed
	}
}
