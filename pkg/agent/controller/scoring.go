package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
)

// scoringOutputSchema instructs the LLM to end its response with the total score
// on the last line. The controller parses this to extract the numeric score.
const scoringOutputSchema = `End your response with the total score as a standalone number on the very last line.
The last line must contain ONLY the number — no formatting, no markdown, no text.
Example: if the total score is 62, the last line should be exactly:
62`

// ScoringResult holds the structured output of a scoring evaluation.
type ScoringResult struct {
	TotalScore            int      `json:"total_score"`
	ScoreAnalysis         string   `json:"score_analysis"`
	ToolImprovementReport string   `json:"tool_improvement_report"`
	FailureTags           []string `json:"failure_tags"`
}

// ScoringController conducts a multi-turn LLM conversation to evaluate
// session quality and extract a score. Stateless — all state comes from
// parameters. It persists LLM interactions and timeline events so scoring
// data is visible in the trace API.
//
// Supports LLM provider fallback: if a call fails with a retryable error,
// the controller switches to the next configured fallback provider (using
// SingleShot thresholds) and retries the failed call.
type ScoringController struct{}

// NewScoringController creates a new scoring controller.
func NewScoringController() *ScoringController {
	return &ScoringController{}
}

// scoringCallLLM wraps callLLMWithStreaming with fallback retry.
// On provider failure it tries the next fallback provider before giving up.
func scoringCallLLM(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	fbState *FallbackState,
	messages []agent.ConversationMessage,
	eventSeq *int,
) (*StreamedResponse, error) {
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		llmStart := time.Now()
		streamed, err := callLLMWithStreaming(ctx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
			SessionID:   execCtx.SessionID,
			ExecutionID: execCtx.ExecutionID,
			Messages:    messages,
			Config:      execCtx.Config.LLMProvider,
			Backend:     execCtx.Config.LLMBackend,
			ClearCache:  fbState.consumeClearCache(),
		}, eventSeq)
		metrics.ObserveLLMCall(execCtx.Config.LLMProviderName, execCtx.Config.LLMProvider.Model,
			time.Since(llmStart), metricsTokens(streamed, err), err)
		if err == nil {
			return streamed, nil
		}
		if !tryFallback(ctx, execCtx, fbState, err, eventSeq) {
			return nil, err
		}
	}
}

// scoreRegex matches a standalone integer (with optional sign) on a line.
var scoreRegex = regexp.MustCompile(`^\s*([+-]?\d+)\s*$`)

const (
	// maxExtractionRetries is the number of times we try to persuade the LLM to give us the total score
	// in the manner described by the scoringOutputSchema. It doesn't make sense to make this configurable
	// because the output depends on the contents of the context window of the LLM and the kind of LLM used.
	// It also doesn't make sense to turn this into a time test (e.g. retry with exp. backoff) because
	// the output of the LLM depends on the contents of the context window, not the time elapsed since
	// we asked the same question last.
	// So, let's just hardcode a "sufficiently large" number that should suffice. If the LLM cannot adhere
	// to relatively simple instructions 5 times in a row, there's something wrong with the analysis as
	// a whole and it makes more sense to retry the whole scoring process.
	maxExtractionRetries = 5
)

// Run executes the scoring evaluation: a score evaluation turn followed by a
// tool improvement report turn. Returns the result as JSON in FinalAnalysis.
func (c *ScoringController) Run(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) (*agent.ExecutionResult, error) {
	if execCtx == nil {
		return nil, fmt.Errorf("execCtx is nil")
	}
	if execCtx.Config == nil {
		return nil, fmt.Errorf("execCtx.Config is nil: cannot read LLM configuration")
	}
	if execCtx.Config.LLMProvider == nil {
		return nil, fmt.Errorf("execCtx.Config.LLMProvider is nil: cannot determine LLM provider")
	}
	if execCtx.PromptBuilder == nil {
		return nil, fmt.Errorf("PromptBuilder is nil: cannot build scoring prompts")
	}
	if execCtx.LLMClient == nil {
		return nil, fmt.Errorf("LLMClient is nil: cannot call LLM for scoring")
	}

	var totalUsage agent.TokenUsage
	eventSeq := 0
	msgSeq := 0
	iteration := 1
	fbState := NewFallbackState(execCtx)
	fbState.SingleShot = true

	// --- Turn 1: Score evaluation ---

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: execCtx.PromptBuilder.BuildScoringSystemPrompt()},
		{Role: agent.RoleUser, Content: execCtx.PromptBuilder.BuildScoringInitialPrompt(prevStageContext, scoringOutputSchema)},
	}

	if err := storeMessages(ctx, execCtx, messages, &msgSeq); err != nil {
		return nil, fmt.Errorf("failed to store initial scoring messages: %w", err)
	}

	startTime := time.Now()
	streamed, err := scoringCallLLM(ctx, execCtx, fbState, messages, &eventSeq)
	if err != nil {
		return nil, fmt.Errorf("scoring LLM call failed: %w", err)
	}
	resp := streamed.LLMResponse
	accumulateUsage(&totalUsage, resp)

	assistantMsg, storeErr := storeAssistantMessage(ctx, execCtx, resp, &msgSeq)
	if storeErr != nil {
		return nil, fmt.Errorf("failed to store scoring assistant message: %w", storeErr)
	}
	recordLLMInteraction(ctx, execCtx, iteration, llminteraction.InteractionTypeScoring, len(messages), resp, &assistantMsg.ID, startTime)
	iteration++

	// Extract score from the response text.
	// Preserve the best analysis across retries: a score-only retry ("67")
	// returns empty analysis, but the original response already contained the
	// full rationale we need for score_analysis and failure_tags.
	score, analysis, err := extractScore(resp.Text)
	bestAnalysis := analysis
	if err != nil {
		bestAnalysis = strings.TrimRight(resp.Text, "\n\r ")
	}

	// Retry extraction if parsing fails
	for attempt := 0; err != nil && attempt < maxExtractionRetries; attempt++ {
		retryPrompt := execCtx.PromptBuilder.BuildScoringOutputSchemaReminderPrompt(scoringOutputSchema)
		messages = append(messages,
			agent.ConversationMessage{Role: agent.RoleAssistant, Content: resp.Text},
			agent.ConversationMessage{Role: agent.RoleUser, Content: retryPrompt},
		)
		storeObservationMessage(ctx, execCtx, retryPrompt, &msgSeq)

		startTime = time.Now()
		streamed, err = scoringCallLLM(ctx, execCtx, fbState, messages, &eventSeq)
		if err != nil {
			return nil, fmt.Errorf("scoring extraction retry LLM call failed: %w", err)
		}
		resp = streamed.LLMResponse
		accumulateUsage(&totalUsage, resp)

		assistantMsg, storeErr = storeAssistantMessage(ctx, execCtx, resp, &msgSeq)
		if storeErr != nil {
			return nil, fmt.Errorf("failed to store scoring retry assistant message: %w", storeErr)
		}
		recordLLMInteraction(ctx, execCtx, iteration, llminteraction.InteractionTypeScoring, len(messages), resp, &assistantMsg.ID, startTime)
		iteration++
		score, analysis, err = extractScore(resp.Text)
		if analysis != "" {
			bestAnalysis = analysis
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to extract score after retries: %w", err)
	}
	if analysis == "" {
		analysis = bestAnalysis
	}

	failureTags := scanFailureTags(analysis)

	// --- Turn 2: Tool improvement report ---

	toolReportPrompt := execCtx.PromptBuilder.BuildScoringToolImprovementReportPrompt()
	messages = append(messages,
		agent.ConversationMessage{Role: agent.RoleAssistant, Content: resp.Text},
		agent.ConversationMessage{Role: agent.RoleUser, Content: toolReportPrompt},
	)
	storeObservationMessage(ctx, execCtx, toolReportPrompt, &msgSeq)

	startTime = time.Now()
	toolReportStreamed, err := scoringCallLLM(ctx, execCtx, fbState, messages, &eventSeq)
	if err != nil {
		return nil, fmt.Errorf("tool improvement report LLM call failed: %w", err)
	}
	toolReportResp := toolReportStreamed.LLMResponse
	accumulateUsage(&totalUsage, toolReportResp)

	toolReportMsg, storeErr := storeAssistantMessage(ctx, execCtx, toolReportResp, &msgSeq)
	if storeErr != nil {
		return nil, fmt.Errorf("failed to store tool improvement report assistant message: %w", storeErr)
	}
	recordLLMInteraction(ctx, execCtx, iteration, llminteraction.InteractionTypeScoring, len(messages), toolReportResp, &toolReportMsg.ID, startTime)

	// --- Build result ---

	result := ScoringResult{
		TotalScore:            score,
		ScoreAnalysis:         analysis,
		ToolImprovementReport: toolReportResp.Text,
		FailureTags:           failureTags,
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal scoring result: %w", err)
	}

	return &agent.ExecutionResult{
		Status:        agent.ExecutionStatusCompleted,
		FinalAnalysis: string(resultJSON),
		TokensUsed:    totalUsage,
	}, nil
}

// extractScore parses the LLM response to extract the numeric score from the
// last line and the analysis from all preceding lines.
func extractScore(text string) (score int, analysis string, err error) {
	text = strings.TrimRight(text, "\n\r ")
	if text == "" {
		return 0, "", fmt.Errorf("empty response text")
	}

	// Find score on the last line
	lastNewline := strings.LastIndex(text, "\n")
	var lastLine string
	if lastNewline == -1 {
		lastLine = text
	} else {
		lastLine = text[lastNewline+1:]
	}

	match := scoreRegex.FindStringSubmatch(lastLine)
	if match == nil {
		return 0, "", fmt.Errorf("no numeric score found on last line: %q", lastLine)
	}

	score, err = strconv.Atoi(match[1])
	if err != nil {
		return 0, "", fmt.Errorf("failed to parse score %q: %w", match[1], err)
	}

	if score < 0 || score > 100 {
		return 0, "", fmt.Errorf("score %d out of valid range [0, 100]", score)
	}

	// Analysis is everything before the last line
	if lastNewline == -1 {
		analysis = ""
	} else {
		analysis = text[:lastNewline]
	}

	return score, analysis, nil
}

// scanFailureTags scans the score analysis text for failure vocabulary terms.
// Returns matched terms in vocabulary order. Always returns a non-nil slice
// so JSON marshaling produces [] instead of null.
func scanFailureTags(analysis string) []string {
	tags := make([]string, 0)
	for _, ft := range prompt.FailureVocabulary {
		if strings.Contains(analysis, ft.Term) {
			tags = append(tags, ft.Term)
		}
	}
	return tags
}
