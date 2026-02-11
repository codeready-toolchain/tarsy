package context

import (
	"fmt"
	"strings"
)

// StageResult holds the output of a completed stage for context building.
// Populated by the executor from in-memory stageResult (no DB query needed —
// FinalAnalysis flows through the chain loop via agent.ExecutionResult).
type StageResult struct {
	StageName     string
	FinalAnalysis string
}

// BuildStageContext formats completed stage results into a context string
// for the next stage's agent prompt. Each stage's final analysis is included
// with its stage name as a header.
//
// The returned string is passed as prevStageContext to Agent.Execute() and
// wrapped by FormatChainContext() in the prompt builder.
func BuildStageContext(stages []StageResult) string {
	if len(stages) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<!-- CHAIN_CONTEXT_START -->\n\n")

	for i, stage := range stages {
		sb.WriteString(fmt.Sprintf("### Stage %d: %s\n\n", i+1, stage.StageName))
		if stage.FinalAnalysis != "" {
			sb.WriteString(stage.FinalAnalysis)
		} else {
			sb.WriteString("(No final analysis produced)")
		}
		sb.WriteString("\n\n")
	}

	sb.WriteString("<!-- CHAIN_CONTEXT_END -->")
	return sb.String()
}
