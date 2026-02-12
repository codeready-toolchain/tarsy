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
// GET /api/v1/sessions/:id/debug
// Level 1: Interaction list grouped by execution (metadata only).
// ────────────────────────────────────────────────────────────

func (s *Server) getDebugListHandler(c *echo.Context) error {
	sessionID := c.Param("id")
	if sessionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "session id is required")
	}
	if s.interactionService == nil || s.stageService == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "debug endpoints not configured")
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

	resp := buildDebugListResponse(stages, llmInteractions, mcpInteractions)
	return c.JSON(http.StatusOK, resp)
}

// ────────────────────────────────────────────────────────────
// GET /api/v1/sessions/:id/debug/llm/:interaction_id
// Level 2: Full LLM interaction with reconstructed conversation.
// ────────────────────────────────────────────────────────────

func (s *Server) getLLMInteractionHandler(c *echo.Context) error {
	interactionID := c.Param("interaction_id")
	if interactionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "interaction_id is required")
	}
	if s.interactionService == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "debug endpoints not configured")
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
// GET /api/v1/sessions/:id/debug/mcp/:interaction_id
// Level 2: Full MCP interaction details.
// ────────────────────────────────────────────────────────────

func (s *Server) getMCPInteractionHandler(c *echo.Context) error {
	interactionID := c.Param("interaction_id")
	if interactionID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "interaction_id is required")
	}
	if s.interactionService == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "debug endpoints not configured")
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

// buildDebugListResponse groups interactions into the stage → execution hierarchy.
func buildDebugListResponse(
	stages []*ent.Stage,
	llmInteractions []*ent.LLMInteraction,
	mcpInteractions []*ent.MCPInteraction,
) *models.DebugListResponse {
	// Index LLM interactions by execution_id.
	llmByExec := make(map[string][]*ent.LLMInteraction)
	for _, li := range llmInteractions {
		llmByExec[li.ExecutionID] = append(llmByExec[li.ExecutionID], li)
	}

	// Index MCP interactions by execution_id.
	mcpByExec := make(map[string][]*ent.MCPInteraction)
	for _, mi := range mcpInteractions {
		mcpByExec[mi.ExecutionID] = append(mcpByExec[mi.ExecutionID], mi)
	}

	// Build two-level response: stages → executions → interactions.
	var stageGroups []models.DebugStageGroup
	for _, stg := range stages {
		sg := models.DebugStageGroup{
			StageID:   stg.ID,
			StageName: stg.StageName,
		}

		// Eager-loaded agent executions, sorted by agent_index for deterministic order.
		executions := stg.Edges.AgentExecutions
		sort.Slice(executions, func(i, j int) bool {
			return executions[i].AgentIndex < executions[j].AgentIndex
		})

		for _, exec := range executions {
			eg := models.DebugExecutionGroup{
				ExecutionID: exec.ID,
				AgentName:   exec.AgentName,
			}

			// Map LLM interactions to list items.
			for _, li := range llmByExec[exec.ID] {
				eg.LLMInteractions = append(eg.LLMInteractions, toLLMListItem(li))
			}
			if eg.LLMInteractions == nil {
				eg.LLMInteractions = []models.LLMInteractionListItem{}
			}

			// Map MCP interactions to list items.
			for _, mi := range mcpByExec[exec.ID] {
				eg.MCPInteractions = append(eg.MCPInteractions, toMCPListItem(mi))
			}
			if eg.MCPInteractions == nil {
				eg.MCPInteractions = []models.MCPInteractionListItem{}
			}

			sg.Executions = append(sg.Executions, eg)
		}
		if sg.Executions == nil {
			sg.Executions = []models.DebugExecutionGroup{}
		}

		stageGroups = append(stageGroups, sg)
	}
	if stageGroups == nil {
		stageGroups = []models.DebugStageGroup{}
	}

	return &models.DebugListResponse{Stages: stageGroups}
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
		LLMRequest:       li.LlmRequest,
		LLMResponse:      li.LlmResponse,
		ResponseMetadata: li.ResponseMetadata,
		CreatedAt:        li.CreatedAt.Format(time.RFC3339Nano),
		Conversation:     conversation,
	}
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
