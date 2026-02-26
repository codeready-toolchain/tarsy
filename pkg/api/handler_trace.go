package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/schema"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	echo "github.com/labstack/echo/v5"
)

// ────────────────────────────────────────────────────────────
// GET /api/v1/sessions/:id/trace
// Level 1: Interaction list grouped by execution (metadata only).
// ────────────────────────────────────────────────────────────

func (s *Server) getTraceListHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}
	if s.interactionService == nil || s.stageService == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "trace endpoints not configured")
	}

	ctx := c.Request().Context()

	// Load stages with their agent executions.
	stages, err := s.stageService.GetStagesBySession(ctx, sessionID, true)
	if err != nil {
		return mapServiceError(err)
	}

	// Load all LLM and MCP interactions for the session.
	llmInteractions, err := s.interactionService.GetLLMInteractionsList(ctx, sessionID)
	if err != nil {
		return mapServiceError(err)
	}
	mcpInteractions, err := s.interactionService.GetMCPInteractionsList(ctx, sessionID)
	if err != nil {
		return mapServiceError(err)
	}

	resp := buildTraceListResponse(stages, llmInteractions, mcpInteractions)
	return c.JSON(http.StatusOK, resp)
}

// ────────────────────────────────────────────────────────────
// GET /api/v1/sessions/:id/trace/llm/:interaction_id
// Level 2: Full LLM interaction with reconstructed conversation.
// ────────────────────────────────────────────────────────────

func (s *Server) getLLMInteractionHandler(c *echo.Context) error {
	interactionID := c.Param("interaction_id")
	if interactionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "interaction_id is required")
	}
	if s.interactionService == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "trace endpoints not configured")
	}

	ctx := c.Request().Context()

	interaction, err := s.interactionService.GetLLMInteractionDetail(ctx, interactionID)
	if err != nil {
		return mapServiceError(err)
	}

	messages, err := s.interactionService.ReconstructConversation(ctx, interactionID)
	if err != nil {
		return mapServiceError(err)
	}

	resp := toLLMDetailResponse(interaction, messages)
	return c.JSON(http.StatusOK, resp)
}

// ────────────────────────────────────────────────────────────
// GET /api/v1/sessions/:id/trace/mcp/:interaction_id
// Level 2: Full MCP interaction details.
// ────────────────────────────────────────────────────────────

func (s *Server) getMCPInteractionHandler(c *echo.Context) error {
	interactionID := c.Param("interaction_id")
	if interactionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "interaction_id is required")
	}
	if s.interactionService == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "trace endpoints not configured")
	}

	ctx := c.Request().Context()

	interaction, err := s.interactionService.GetMCPInteractionDetail(ctx, interactionID)
	if err != nil {
		return mapServiceError(err)
	}

	resp := toMCPDetailResponse(interaction)
	return c.JSON(http.StatusOK, resp)
}

// ────────────────────────────────────────────────────────────
// Grouping logic (pure function — no HTTP/service dependencies)
// ────────────────────────────────────────────────────────────

// buildTraceListResponse groups interactions into the stage → execution hierarchy.
func buildTraceListResponse(
	stages []*ent.Stage,
	llmInteractions []*ent.LLMInteraction,
	mcpInteractions []*ent.MCPInteraction,
) *models.TraceListResponse {
	// Separate session-level interactions (nil execution_id) from stage-level.
	llmByExec := make(map[string][]*ent.LLMInteraction)
	var sessionLLM []models.LLMInteractionListItem
	for _, li := range llmInteractions {
		if li.ExecutionID == nil {
			sessionLLM = append(sessionLLM, toLLMListItem(li))
		} else {
			llmByExec[*li.ExecutionID] = append(llmByExec[*li.ExecutionID], li)
		}
	}
	if sessionLLM == nil {
		sessionLLM = []models.LLMInteractionListItem{}
	}

	// Index MCP interactions by execution_id.
	mcpByExec := make(map[string][]*ent.MCPInteraction)
	for _, mi := range mcpInteractions {
		mcpByExec[mi.ExecutionID] = append(mcpByExec[mi.ExecutionID], mi)
	}

	// Build two-level response: stages → executions → interactions.
	// Sub-agent executions are nested under their parent orchestrator execution.
	var stageGroups []models.TraceStageGroup
	for _, stg := range stages {
		sg := models.TraceStageGroup{
			StageID:   stg.ID,
			StageName: stg.StageName,
		}

		// Eager-loaded agent executions, sorted by agent_index for deterministic order.
		allExecs := stg.Edges.AgentExecutions
		sort.Slice(allExecs, func(i, j int) bool {
			return allExecs[i].AgentIndex < allExecs[j].AgentIndex
		})

		// Split into top-level and sub-agent executions.
		subByParent := make(map[string][]*ent.AgentExecution)
		var topLevel []*ent.AgentExecution
		for _, exec := range allExecs {
			if exec.ParentExecutionID != nil {
				subByParent[*exec.ParentExecutionID] = append(subByParent[*exec.ParentExecutionID], exec)
			} else {
				topLevel = append(topLevel, exec)
			}
		}

		for _, exec := range topLevel {
			eg := buildTraceExecutionGroup(exec, llmByExec, mcpByExec)

			// Nest sub-agent executions under their parent.
			for _, sub := range subByParent[exec.ID] {
				eg.SubAgents = append(eg.SubAgents, buildTraceExecutionGroup(sub, llmByExec, mcpByExec))
			}

			sg.Executions = append(sg.Executions, eg)
		}
		if sg.Executions == nil {
			sg.Executions = []models.TraceExecutionGroup{}
		}

		stageGroups = append(stageGroups, sg)
	}
	if stageGroups == nil {
		stageGroups = []models.TraceStageGroup{}
	}

	return &models.TraceListResponse{
		Stages:              stageGroups,
		SessionInteractions: sessionLLM,
	}
}

// buildTraceExecutionGroup creates a TraceExecutionGroup for a single execution.
func buildTraceExecutionGroup(
	exec *ent.AgentExecution,
	llmByExec map[string][]*ent.LLMInteraction,
	mcpByExec map[string][]*ent.MCPInteraction,
) models.TraceExecutionGroup {
	eg := models.TraceExecutionGroup{
		ExecutionID: exec.ID,
		AgentName:   exec.AgentName,
	}
	for _, li := range llmByExec[exec.ID] {
		eg.LLMInteractions = append(eg.LLMInteractions, toLLMListItem(li))
	}
	if eg.LLMInteractions == nil {
		eg.LLMInteractions = []models.LLMInteractionListItem{}
	}
	for _, mi := range mcpByExec[exec.ID] {
		eg.MCPInteractions = append(eg.MCPInteractions, toMCPListItem(mi))
	}
	if eg.MCPInteractions == nil {
		eg.MCPInteractions = []models.MCPInteractionListItem{}
	}
	return eg
}

// ────────────────────────────────────────────────────────────
// Mapping helpers
// ────────────────────────────────────────────────────────────

func toLLMListItem(li *ent.LLMInteraction) models.LLMInteractionListItem {
	return models.LLMInteractionListItem{
		ID:              li.ID,
		InteractionType: string(li.InteractionType),
		ModelName:       li.ModelName,
		InputTokens:     li.InputTokens,
		OutputTokens:    li.OutputTokens,
		TotalTokens:     li.TotalTokens,
		DurationMs:      li.DurationMs,
		ErrorMessage:    li.ErrorMessage,
		CreatedAt:       li.CreatedAt.Format(time.RFC3339Nano),
	}
}

func toMCPListItem(mi *ent.MCPInteraction) models.MCPInteractionListItem {
	return models.MCPInteractionListItem{
		ID:              mi.ID,
		InteractionType: string(mi.InteractionType),
		ServerName:      mi.ServerName,
		ToolName:        mi.ToolName,
		DurationMs:      mi.DurationMs,
		ErrorMessage:    mi.ErrorMessage,
		CreatedAt:       mi.CreatedAt.Format(time.RFC3339Nano),
	}
}

func toLLMDetailResponse(li *ent.LLMInteraction, messages []*ent.Message) *models.LLMInteractionDetailResponse {
	// Build conversation from Message records (normal iteration/synthesis path).
	conversation := make([]models.ConversationMessage, 0, len(messages))
	for _, msg := range messages {
		cm := models.ConversationMessage{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
			ToolName:   msg.ToolName,
		}
		// Convert ent schema tool calls to API model tool calls.
		if len(msg.ToolCalls) > 0 {
			cm.ToolCalls = make([]models.MessageToolCall, len(msg.ToolCalls))
			for i, tc := range msg.ToolCalls {
				cm.ToolCalls[i] = schemaToolCallToModel(tc)
			}
		}
		conversation = append(conversation, cm)
	}

	// Fallback: extract inline conversation from llm_request for self-contained
	// interactions (e.g. summarization) that don't use the Message table.
	llmRequest := li.LlmRequest
	if len(conversation) == 0 {
		conversation = extractInlineConversation(llmRequest)
		if len(conversation) > 0 {
			// Strip the inline conversation from the metadata — it's
			// surfaced as the top-level Conversation field.
			llmRequest = make(map[string]any, len(li.LlmRequest))
			for k, v := range li.LlmRequest {
				if k != "conversation" {
					llmRequest[k] = v
				}
			}
		}
	}

	return &models.LLMInteractionDetailResponse{
		ID:               li.ID,
		InteractionType:  string(li.InteractionType),
		ModelName:        li.ModelName,
		ThinkingContent:  li.ThinkingContent,
		InputTokens:      li.InputTokens,
		OutputTokens:     li.OutputTokens,
		TotalTokens:      li.TotalTokens,
		DurationMs:       li.DurationMs,
		ErrorMessage:     li.ErrorMessage,
		LLMRequest:       llmRequest,
		LLMResponse:      li.LlmResponse,
		ResponseMetadata: li.ResponseMetadata,
		CreatedAt:        li.CreatedAt.Format(time.RFC3339Nano),
		Conversation:     conversation,
	}
}

// extractInlineConversation reads conversation messages stored inline in the
// llm_request JSON. Used for self-contained interactions (like summarization)
// whose conversations aren't stored in the Message table.
func extractInlineConversation(llmRequest map[string]any) []models.ConversationMessage {
	rawConv, ok := llmRequest["conversation"]
	if !ok {
		return nil
	}
	convSlice, ok := rawConv.([]any)
	if !ok {
		return nil
	}
	result := make([]models.ConversationMessage, 0, len(convSlice))
	for _, item := range convSlice {
		msgMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msgMap["role"].(string)
		content, _ := msgMap["content"].(string)
		if role == "" {
			continue
		}
		result = append(result, models.ConversationMessage{
			Role:    role,
			Content: content,
		})
	}
	return result
}

func toMCPDetailResponse(mi *ent.MCPInteraction) *models.MCPInteractionDetailResponse {
	return &models.MCPInteractionDetailResponse{
		ID:              mi.ID,
		InteractionType: string(mi.InteractionType),
		ServerName:      mi.ServerName,
		ToolName:        mi.ToolName,
		ToolArguments:   mi.ToolArguments,
		ToolResult:      mi.ToolResult,
		AvailableTools:  mi.AvailableTools,
		DurationMs:      mi.DurationMs,
		ErrorMessage:    mi.ErrorMessage,
		CreatedAt:       mi.CreatedAt.Format(time.RFC3339Nano),
	}
}

func schemaToolCallToModel(tc schema.MessageToolCall) models.MessageToolCall {
	return models.MessageToolCall{
		ID:        tc.ID,
		Name:      tc.Name,
		Arguments: tc.Arguments,
	}
}
