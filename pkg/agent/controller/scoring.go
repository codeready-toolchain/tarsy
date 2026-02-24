package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// scoringOutputSchema instructs the LLM to end its response with the total score
// on the last line. The controller parses this to extract the numeric score.
const scoringOutputSchema = `End your response with the total score as a standalone number on the last line.
For example, if the total score is 62, the last line of your response should be:
62`

// ScoringResult holds the structured output of a scoring evaluation.
type ScoringResult struct {
	TotalScore           int    `json:"total_score"`
	ScoreAnalysis        string `json:"score_analysis"`
	MissingToolsAnalysis string `json:"missing_tools_analysis"`
}

// ScoringController conducts a multi-turn LLM conversation to evaluate
// session quality and extract a score. Stateless — all state comes from
// parameters. It operates outside the investigation data model: no
// Services, no timeline events, no message storage.
type ScoringController struct{}

// NewScoringController creates a new scoring controller.
func NewScoringController() *ScoringController {
	return &ScoringController{}
}

// scoreRegex matches a number (0-100) optionally followed by whitespace at end of string.
var scoreRegex = regexp.MustCompile(`([+-]?\d+)\s*$`)

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
// missing-tools analysis turn. Returns the result as JSON in FinalAnalysis.
func (c *ScoringController) Run(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) (*agent.ExecutionResult, error) {
	if execCtx.PromptBuilder == nil {
		return nil, fmt.Errorf("PromptBuilder is nil: cannot build scoring prompts")
	}
	if execCtx.LLMClient == nil {
		return nil, fmt.Errorf("LLMClient is nil: cannot call LLM for scoring")
	}

	var totalUsage agent.TokenUsage

	// --- Turn 1: Score evaluation ---

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: execCtx.PromptBuilder.BuildScoringSystemPrompt()},
		{Role: agent.RoleUser, Content: execCtx.PromptBuilder.BuildScoringInitialPrompt(prevStageContext, scoringOutputSchema)},
	}

	resp, err := callLLM(ctx, execCtx.LLMClient, llmInput(execCtx, messages))
	if err != nil {
		return nil, fmt.Errorf("scoring LLM call failed: %w", err)
	}
	accumulateUsage(&totalUsage, resp)

	// Extract score from the response text
	score, analysis, err := extractScore(resp.Text)

	// Retry extraction if parsing fails
	for attempt := 0; err != nil && attempt < maxExtractionRetries; attempt++ {
		messages = append(messages,
			agent.ConversationMessage{Role: agent.RoleAssistant, Content: resp.Text},
			agent.ConversationMessage{Role: agent.RoleUser, Content: execCtx.PromptBuilder.BuildScoringOutputSchemaReminderPrompt(scoringOutputSchema)},
		)

		resp, err = callLLM(ctx, execCtx.LLMClient, llmInput(execCtx, messages))
		if err != nil {
			return nil, fmt.Errorf("scoring extraction retry LLM call failed: %w", err)
		}
		accumulateUsage(&totalUsage, resp)
		score, analysis, err = extractScore(resp.Text)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to extract score after retries: %w", err)
	}

	// --- Turn 2: Missing tools analysis ---

	messages = append(messages,
		agent.ConversationMessage{Role: agent.RoleAssistant, Content: resp.Text},
		agent.ConversationMessage{Role: agent.RoleUser, Content: execCtx.PromptBuilder.BuildScoringMissingToolsReportPrompt()},
	)

	missingToolsResp, err := callLLM(ctx, execCtx.LLMClient, llmInput(execCtx, messages))
	if err != nil {
		return nil, fmt.Errorf("missing tools LLM call failed: %w", err)
	}
	accumulateUsage(&totalUsage, missingToolsResp)

	// --- Build result ---

	result := ScoringResult{
		TotalScore:           score,
		ScoreAnalysis:        analysis,
		MissingToolsAnalysis: missingToolsResp.Text,
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

func llmInput(execCtx *agent.ExecutionContext, messages []agent.ConversationMessage) *agent.GenerateInput {
	return &agent.GenerateInput{
		SessionID:   execCtx.SessionID,
		ExecutionID: execCtx.ExecutionID,
		Messages:    messages,
		Config:      execCtx.Config.LLMProvider,
		Tools:       nil,
		Backend:     execCtx.Config.LLMBackend,
	}
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

	// Analysis is everything before the last line
	if lastNewline == -1 {
		analysis = ""
	} else {
		analysis = text[:lastNewline]
	}

	return score, analysis, nil
}
