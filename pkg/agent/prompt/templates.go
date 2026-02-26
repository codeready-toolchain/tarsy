// Package prompt provides the centralized prompt builder framework for all
// agent controllers. It composes system messages, user messages, instruction
// hierarchies, and strategy-specific formatting.
package prompt

// separator is a visual delimiter for prompt sections.
const separator = "═══════════════════════════════════════════════════════════════════════════════"

// analysisTask is the investigation task instruction appended to the user message.
const analysisTask = `## Your Task
Use the available tools to investigate this alert and provide:
1. Root cause analysis
2. Current system state assessment
3. Specific remediation steps for human operators
4. Prevention recommendations

Be thorough in your investigation before providing the final answer.`

// synthesisTask is the synthesis task instruction for combining parallel results.
const synthesisTask = `Synthesize the investigation results and provide your comprehensive analysis.`

// forcedConclusionTemplate is the base template for forced conclusion prompts.
// %d = iteration count, %s = format instructions.
const forcedConclusionTemplate = `You have reached the investigation iteration limit (%d iterations).

Please conclude your investigation by answering the original question based on what you've discovered.

**Conclusion guidance:**
- Use the data and observations you've already gathered
- Perfect information is not required - provide actionable insights from available findings
- If gaps remain, clearly state what you couldn't determine and why
- Clearly distinguish between conclusions supported by tool-gathered evidence and those based only on the original alert data
- If most tool calls failed, returned errors, or produced no meaningful data, explicitly state that your analysis is limited and primarily based on alert data
- Focus on practical next steps based on current knowledge

%s`

// forcedConclusionFormat is the forced conclusion format instruction.
const forcedConclusionFormat = `Provide a clear, structured conclusion that directly addresses the investigation question.`

// mcpSummarizationSystemTemplate is the system prompt for MCP result summarization.
// %s = server name, %s = tool name, %d = max summary tokens.
const mcpSummarizationSystemTemplate = `You are an expert at summarizing technical output from system administration and monitoring tools for ongoing incident investigation.

Your specific task is to summarize output from **%s.%s** in a way that:

1. **Preserves Critical Information**: Keep all details essential for troubleshooting and investigation
2. **Maintains Investigation Context**: Focus on information relevant to what the investigator was looking for
3. **Reduces Verbosity**: Remove redundant details while preserving technical accuracy
4. **Highlights Key Findings**: Emphasize errors, warnings, unusual patterns, and actionable insights
5. **Stays Concise**: Keep summary under %d tokens while preserving meaning

## Summarization Guidelines:

- **Always Preserve**: Error messages, warnings, status indicators, resource metrics, timestamps
- **Intelligently Summarize**: Large lists by showing patterns, counts, and notable exceptions
- **Focus On**: Non-default configurations, problematic settings, resource utilization issues
- **Maintain**: Technical accuracy and context about what the data represents
- **Format**: Clean, structured text suitable for continued technical investigation
- **Be Conclusive**: Explicitly state what was found AND what was NOT found to prevent re-queries
- **Answer Questions**: If the investigation context suggests the investigator was looking for something specific, explicitly confirm whether it was present or absent

Your summary will be inserted as an observation in the ongoing investigation conversation.`

// mcpSummarizationUserTemplate is the user prompt for MCP result summarization.
// %s = conversation context, %s = server name, %s = tool name, %s = result text.
const mcpSummarizationUserTemplate = `Below is the ongoing investigation conversation that provides context for what the investigator has been looking for:

## Investigation Context:
=== CONVERSATION START ===
%s
=== CONVERSATION END ===

## Tool Result to Summarize:
The investigator just executed ` + "`%s.%s`" + ` and got the following output:

=== TOOL OUTPUT START ===
%s
=== TOOL OUTPUT END ===

## Your Task:
Based on the investigation context above, provide a concise summary of the tool result that:
- Preserves information most relevant to what the investigator was looking for
- Removes verbose or redundant details that don't impact the investigation
- Maintains technical accuracy and actionable insights
- Fits naturally as the next observation in the investigation conversation

CRITICAL INSTRUCTION: Return ONLY the summary text. Do NOT include "Final Answer:", "Thought:", "Action:", or any other formatting.`

// executiveSummarySystemPrompt is the system prompt for executive summary generation.
const executiveSummarySystemPrompt = `You are an expert Site Reliability Engineer assistant that creates concise 1-4 line executive summaries of incident analyses for alert notifications. Focus on clarity, brevity, and actionable information.`

// executiveSummaryUserTemplate is the user prompt for executive summary generation.
// %s = final analysis text.
const executiveSummaryUserTemplate = `Generate a 1-4 line executive summary of this incident analysis.

CRITICAL RULES:
- Only summarize what is EXPLICITLY stated in the analysis
- Do NOT infer future actions or recommendations not mentioned
- Do NOT add your own conclusions
- Focus on: what happened, current status, and ONLY stated next steps

Analysis to summarize:

=================================================================================
%s
=================================================================================

Executive Summary (1-4 lines, facts only):`
