package queue

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	agentctx "github.com/codeready-toolchain/tarsy/pkg/agent/context"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/runbook"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// InvestigationContextBuilder reconstructs the full investigation context
// (alert data, runbook, tool inventory, timeline) for a session. Used by
// both the scoring Reflector and the feedback Reflector so both get the
// same rich context for memory extraction.
type InvestigationContextBuilder struct {
	cfg             *config.Config
	dbClient        *ent.Client
	stageService    *services.StageService
	timelineService *services.TimelineService
	runbookService  *runbook.Service
}

// NewInvestigationContextBuilder creates a builder from the services
// ScoringExecutor already has.
func NewInvestigationContextBuilder(
	cfg *config.Config,
	dbClient *ent.Client,
	stageService *services.StageService,
	timelineService *services.TimelineService,
	runbookService *runbook.Service,
) *InvestigationContextBuilder {
	return &InvestigationContextBuilder{
		cfg:             cfg,
		dbClient:        dbClient,
		stageService:    stageService,
		timelineService: timelineService,
		runbookService:  runbookService,
	}
}

// Build returns the full investigation context string for a session.
func (b *InvestigationContextBuilder) Build(ctx context.Context, session *ent.AlertSession) string {
	var sb strings.Builder

	sb.WriteString("## ORIGINAL ALERT\n\n")
	if session.AlertData != "" {
		sb.WriteString(session.AlertData)
	} else {
		sb.WriteString("(No alert data available)")
	}
	sb.WriteString("\n\n")

	runbookContent := b.resolveRunbook(ctx, session)
	if runbookContent != "" {
		sb.WriteString("## RUNBOOK\n\n")
		sb.WriteString(runbookContent)
		sb.WriteString("\n\n")
	}

	timeline, toolsByExec := b.buildInvestigationData(ctx, session.ID)

	if len(toolsByExec) > 0 {
		sb.WriteString("## AVAILABLE TOOLS PER AGENT\n\n")
		sb.WriteString(toolsByExec)
	}

	sb.WriteString("## INVESTIGATION TIMELINE\n\n")
	sb.WriteString(timeline)

	return sb.String()
}

func (b *InvestigationContextBuilder) buildInvestigationData(ctx context.Context, sessionID string) (timeline string, toolsSection string) {
	logger := slog.With("session_id", sessionID)

	stages, err := b.stageService.GetStagesBySession(ctx, sessionID, true)
	if err != nil {
		logger.Warn("Failed to get stages for investigation context", "error", err)
		return "", ""
	}

	synthResults := make(map[string]string)
	for _, stg := range stages {
		if stg.StageType == stage.StageTypeSynthesis && stg.ReferencedStageID != nil {
			if fa := extractFinalAnalysisFromStage(ctx, b.timelineService, stg); fa != "" {
				synthResults[*stg.ReferencedStageID] = fa
			}
		}
	}

	type agentTools struct {
		name  string
		tools string
	}
	var allAgentTools []agentTools

	var investigations []agentctx.StageInvestigation
	for _, stg := range stages {
		switch stg.StageType {
		case stage.StageTypeInvestigation, stage.StageTypeExecSummary, stage.StageTypeAction:
		default:
			continue
		}

		execs := stg.Edges.AgentExecutions
		sort.Slice(execs, func(i, j int) bool {
			return execs[i].AgentIndex < execs[j].AgentIndex
		})
		agents := make([]agentctx.AgentInvestigation, len(execs))
		for i, exec := range execs {
			var tlEvents []*ent.TimelineEvent
			tl, tlErr := b.timelineService.GetAgentTimeline(ctx, exec.ID)
			if tlErr != nil {
				logger.Warn("Failed to get agent timeline for investigation context",
					"execution_id", exec.ID, "error", tlErr)
			} else {
				tlEvents = tl
			}

			agents[i] = agentctx.AgentInvestigation{
				AgentName:    exec.AgentName,
				AgentIndex:   exec.AgentIndex,
				LLMBackend:   exec.LlmBackend,
				LLMProvider:  stringFromNillable(exec.LlmProvider),
				Status:       mapExecStatusToSessionStatus(exec.Status),
				Events:       tlEvents,
				ErrorMessage: stringFromNillable(exec.ErrorMessage),
			}

			if toolsStr := b.formatAgentTools(ctx, exec.ID); toolsStr != "" {
				allAgentTools = append(allAgentTools, agentTools{name: exec.AgentName, tools: toolsStr})
			}
		}

		si := agentctx.StageInvestigation{
			StageName:  stg.StageName,
			StageIndex: stg.StageIndex,
			Agents:     agents,
		}
		if synth, ok := synthResults[stg.ID]; ok {
			si.SynthesisResult = synth
		}
		investigations = append(investigations, si)
	}

	executiveSummary := b.getExecutiveSummary(ctx, sessionID)
	timeline = agentctx.FormatStructuredInvestigation(investigations, executiveSummary)

	merged := make(map[string]string)
	var order []string
	for _, at := range allAgentTools {
		if _, exists := merged[at.name]; !exists {
			order = append(order, at.name)
			merged[at.name] = at.tools
		} else {
			merged[at.name] += at.tools
		}
	}
	var sb strings.Builder
	for _, name := range order {
		sb.WriteString("### ")
		sb.WriteString(name)
		sb.WriteString("\n\n")
		sb.WriteString(merged[name])
		sb.WriteString("\n")
	}

	return timeline, sb.String()
}

func (b *InvestigationContextBuilder) formatAgentTools(ctx context.Context, executionID string) string {
	interactions, err := b.dbClient.MCPInteraction.Query().
		Where(
			mcpinteraction.ExecutionIDEQ(executionID),
			mcpinteraction.InteractionTypeEQ(mcpinteraction.InteractionTypeToolList),
		).
		Order(ent.Asc(mcpinteraction.FieldServerName)).
		All(ctx)
	if err != nil || len(interactions) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, mi := range interactions {
		for _, rawTool := range mi.AvailableTools {
			toolMap, ok := rawTool.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := toolMap["name"].(string)
			desc, _ := toolMap["description"].(string)
			if name == "" {
				continue
			}
			sb.WriteString("- ")
			sb.WriteString(mi.ServerName)
			sb.WriteString(".")
			sb.WriteString(name)
			if desc != "" {
				sb.WriteString(" — ")
				sb.WriteString(desc)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (b *InvestigationContextBuilder) resolveRunbook(ctx context.Context, session *ent.AlertSession) string {
	configDefault := ""
	if b.cfg.Defaults != nil {
		configDefault = b.cfg.Defaults.Runbook
	}

	if b.runbookService == nil {
		return configDefault
	}

	alertURL := ""
	if session.RunbookURL != nil {
		alertURL = *session.RunbookURL
	}

	content, err := b.runbookService.Resolve(ctx, alertURL)
	if err != nil {
		slog.Warn("Investigation context runbook resolution failed, using default",
			"session_id", session.ID, "error", err)
		return configDefault
	}
	return content
}

func (b *InvestigationContextBuilder) getExecutiveSummary(ctx context.Context, sessionID string) string {
	sessionEvents, err := b.timelineService.GetSessionTimeline(ctx, sessionID)
	if err != nil {
		return ""
	}
	for _, evt := range sessionEvents {
		if evt.EventType == timelineevent.EventTypeExecutiveSummary {
			return evt.Content
		}
	}
	return ""
}
