package controller

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// ParsedReActResponse is the result of parsing an LLM response in ReAct format.
type ParsedReActResponse struct {
	// Thinking/reasoning text (everything before Action or Final Answer)
	Thought string

	// Action fields (populated when the LLM wants to call a tool)
	HasAction   bool
	Action      string // Tool name (e.g., "kubectl.get_pods")
	ActionInput string // Tool arguments (raw text)

	// Final answer (populated when the LLM wants to conclude)
	IsFinalAnswer bool
	FinalAnswer   string

	// Error tracking
	IsUnknownTool bool   // Tool name not in available tools
	IsMalformed   bool   // Response doesn't match expected format
	ErrorMessage  string // Specific error description for LLM feedback

	// Diagnostics — tracks what was detected during parsing
	FoundSections map[string]bool
}

// Regex patterns for mid-line detection (compiled once).
var (
	// Match sentence ending (. ! ?) + optional space/backtick/closing-markup + section header
	midlineActionPattern      = regexp.MustCompile(`[.!?][\x60\s*]*Action:`)
	midlineFinalAnswerPattern = regexp.MustCompile(`[.!?][\x60\s*]*Final Answer:`)
	midlineActionInputPattern = regexp.MustCompile(`[.!?][\x60\s*]*Action Input:`)
	// server.tool format validation
	toolNamePattern = regexp.MustCompile(`^([\w\-]+)\.([\w\-]+)$`)
	// Recovery patterns for recoverMissingAction (compiled once)
	recoverActionColonPattern  = regexp.MustCompile(`(?i)\bAction:`)
	recoverActionWordPattern   = regexp.MustCompile(`(?i)\bAction(?:\s|$)`)
	recoverActionInputPattern  = regexp.MustCompile(`(?i)Action Input:`)
)

// ParseReActResponse parses LLM text output into a structured ReAct response.
// The parser is intentionally forgiving — it tries multiple detection strategies
// before declaring a response malformed. All recovery logic from old TARSy's
// react_parser.py is preserved.
func ParseReActResponse(text string) *ParsedReActResponse {
	if text == "" {
		return &ParsedReActResponse{
			IsMalformed: true,
			FoundSections: map[string]bool{
				"thought":      false,
				"action":       false,
				"action_input": false,
				"final_answer": false,
			},
		}
	}

	sections := extractSections(text)

	foundSections := map[string]bool{
		"thought":      sections["thought"] != nil,
		"action":       sections["action"] != nil,
		"action_input": sections["action_input"] != nil,
		"final_answer": sections["final_answer"] != nil,
	}

	action := deref(sections["action"])
	actionInput := sections["action_input"]

	// If both Final Answer AND Action+ActionInput exist, prefer Action
	// (Final Answer should be terminal — nothing should come after it)
	if action != "" && actionInput != nil {
		action = strings.TrimSpace(action)
		if action == "" {
			return &ParsedReActResponse{
				IsMalformed:   true,
				Thought:       deref(sections["thought"]),
				FoundSections: foundSections,
			}
		}

		// Quick-reject: tool name must contain at least one dot.
		// This is intentionally looser than toolNamePattern (which enforces strict
		// ^server.tool$ format). Names like ".tool" or "a.b.c" pass here and are
		// handed to the controller's tool-name-set lookup for final validation.
		// This two-tier approach avoids giving a misleading "must be server.tool
		// format" error for names that almost match the format.
		if !strings.Contains(action, ".") {
			return &ParsedReActResponse{
				IsUnknownTool: true,
				HasAction:     true,
				Thought:       deref(sections["thought"]),
				Action:        action,
				ActionInput:   deref(actionInput),
				ErrorMessage: fmt.Sprintf(
					"Unknown tool '%s'. Tools must be in 'server.tool' format. "+
						"Please check the list of available tools provided in the prompt.", action),
				FoundSections: foundSections,
			}
		}

		return &ParsedReActResponse{
			HasAction:     true,
			Thought:       deref(sections["thought"]),
			Action:        action,
			ActionInput:   deref(actionInput),
			FoundSections: foundSections,
		}
	}

	// Check for final answer (only if no valid action was found)
	if sections["final_answer"] != nil && deref(sections["final_answer"]) != "" {
		return &ParsedReActResponse{
			IsFinalAnswer: true,
			Thought:       deref(sections["thought"]),
			FinalAnswer:   deref(sections["final_answer"]),
			FoundSections: foundSections,
		}
	}

	// Malformed response
	return &ParsedReActResponse{
		IsMalformed:   true,
		Thought:       deref(sections["thought"]),
		FoundSections: foundSections,
	}
}

// extractSections parses the response text into sections.
// Implements the same line-by-line state machine as old TARSy's _extract_sections.
func extractSections(text string) map[string]*string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	parsed := map[string]*string{
		"thought":      nil,
		"action":       nil,
		"action_input": nil,
		"final_answer": nil,
	}

	var currentSection string
	var contentLines []string
	foundSections := map[string]bool{}

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)

		// Skip empty lines when not in a section
		if line == "" && currentSection == "" {
			continue
		}

		// Check for stop conditions first
		if shouldStopParsing(line) {
			finalizeSection(parsed, currentSection, contentLines)
			break
		}

		// Handle Final Answer (can appear at any time)
		if isSectionHeader(line, "final_answer", foundSections) {
			// If in thought and Final Answer appears mid-line, capture thought before it
			if currentSection == "thought" && hasMidlineFinalAnswer(line) {
				loc := midlineFinalAnswerPattern.FindStringIndex(line)
				if loc != nil {
					thoughtBefore := strings.TrimSpace(line[:loc[0]+1])
					if thoughtBefore != "" {
						contentLines = append(contentLines, thoughtBefore)
					}
				}
			}

			finalizeSection(parsed, currentSection, contentLines)
			currentSection = "final_answer"
			foundSections["final_answer"] = true
			contentLines = []string{extractSectionContent(line, "Final Answer:")}

		} else if isSectionHeader(line, "thought", foundSections) {
			finalizeSection(parsed, currentSection, contentLines)
			currentSection = "thought"
			foundSections["thought"] = true

			if strings.HasPrefix(line, "Thought:") {
				thoughtContent := extractSectionContent(line, "Thought:")

				// Check for mid-line Final Answer in thought
				if hasMidlineFinalAnswer(thoughtContent) {
					loc := midlineFinalAnswerPattern.FindStringIndex(thoughtContent)
					if loc != nil {
						thoughtBefore := strings.TrimSpace(thoughtContent[:loc[0]+1])
						setSection(parsed, "thought", thoughtBefore)
						remaining := strings.TrimSpace(thoughtContent[loc[0]+1:])
						if idx := strings.Index(remaining, "Final Answer:"); idx != -1 {
							fa := strings.TrimSpace(remaining[idx+len("Final Answer:"):])
							setSection(parsed, "final_answer", fa)
							foundSections["final_answer"] = true
						}
						currentSection = "final_answer"
						contentLines = []string{deref(parsed["final_answer"])}
					} else {
						contentLines = []string{thoughtContent}
					}
				} else if hasMidlineAction(thoughtContent) {
					// Check for mid-line Action in thought
					loc := midlineActionPattern.FindStringIndex(thoughtContent)
					if loc != nil {
						thoughtBefore := strings.TrimSpace(thoughtContent[:loc[0]+1])
						setSection(parsed, "thought", thoughtBefore)
						remaining := strings.TrimSpace(thoughtContent[loc[0]+1:])
						if idx := strings.Index(remaining, "Action:"); idx != -1 {
							actionVal := strings.TrimSpace(remaining[idx+len("Action:"):])
							setSection(parsed, "action", actionVal)
							foundSections["action"] = true
						}
						currentSection = ""
						contentLines = nil
					} else {
						contentLines = []string{thoughtContent}
					}
				} else {
					contentLines = []string{thoughtContent}
				}
			} else {
				// "Thought" without colon (exact match) — content on next lines
				contentLines = []string{}
			}

		} else if isSectionHeader(line, "action", foundSections) {
			finalizeSection(parsed, currentSection, contentLines)
			currentSection = "action"
			foundSections["action"] = true
			// Clear action_input to allow new action_input after new action
			delete(foundSections, "action_input")
			contentLines = []string{extractSectionContent(line, "Action:")}

		} else if isSectionHeader(line, "action_input", foundSections) {
			finalizeSection(parsed, currentSection, contentLines)
			currentSection = "action_input"
			foundSections["action_input"] = true
			contentLines = []string{extractSectionContent(line, "Action Input:")}

		} else {
			// Only add content if we're in a valid section
			if currentSection != "" {
				// Check for mid-line Final Answer in thought
				if currentSection == "thought" && hasMidlineFinalAnswer(line) {
					loc := midlineFinalAnswerPattern.FindStringIndex(line)
					if loc != nil {
						thoughtBefore := strings.TrimSpace(line[:loc[0]+1])
						if thoughtBefore != "" {
							contentLines = append(contentLines, thoughtBefore)
						}
						finalizeSection(parsed, currentSection, contentLines)
						remaining := strings.TrimSpace(line[loc[0]+1:])
						if idx := strings.Index(remaining, "Final Answer:"); idx != -1 {
							fa := strings.TrimSpace(remaining[idx+len("Final Answer:"):])
							setSection(parsed, "final_answer", fa)
							foundSections["final_answer"] = true
							currentSection = "final_answer"
							contentLines = []string{deref(parsed["final_answer"])}
						}
					} else {
						contentLines = append(contentLines, line)
					}
				} else {
					contentLines = append(contentLines, line)
				}
			}
		}
	}

	// Handle last section
	finalizeSection(parsed, currentSection, contentLines)

	// Recovery mechanism — if we have Action Input but no Action, attempt backtracking
	if parsed["action_input"] != nil && parsed["action"] == nil {
		if recovered := recoverMissingAction(text); recovered != "" {
			setSection(parsed, "action", recovered)
		}
	}

	return parsed
}

// isSectionHeader checks if a line is a valid section header using 3-tier detection.
func isSectionHeader(line string, sectionType string, foundSections map[string]bool) bool {
	if line == "" {
		return false
	}

	// Prevent duplicate final_answer (first one wins)
	if sectionType == "final_answer" && foundSections["final_answer"] {
		return false
	}

	// TIER 1: Standard format (line starts with header)
	switch sectionType {
	case "thought":
		if strings.HasPrefix(line, "Thought:") || line == "Thought" {
			return true
		}
	case "action":
		if strings.HasPrefix(line, "Action:") {
			return true
		}
	case "action_input":
		if strings.HasPrefix(line, "Action Input:") {
			return true
		}
	case "final_answer":
		if strings.HasPrefix(line, "Final Answer:") {
			return true
		}
	}

	// TIER 2: Mid-line Final Answer detection
	if sectionType == "final_answer" {
		// Don't detect mid-line Final Answer if line starts with another section header.
		// "Thought " (with space, no colon) is included to match old TARSy behavior —
		// prevents false positives on lines like "Thought something. Final Answer: ..."
		if strings.HasPrefix(line, "Thought:") || line == "Thought" ||
			strings.HasPrefix(line, "Thought ") ||
			strings.HasPrefix(line, "Action:") || strings.HasPrefix(line, "Action Input:") {
			return false
		}
		if strings.Contains(line, "Final Answer:") && midlineFinalAnswerPattern.MatchString(line) {
			return true
		}
		return false
	}

	// TIER 3: Fallback for Action — mid-line after sentence boundary
	if sectionType == "action" && strings.Contains(line, "Action:") {
		if midlineActionPattern.MatchString(line) {
			return true
		}
	}

	// TIER 3: Fallback for Action Input — only if Action was already found
	if sectionType == "action_input" && strings.Contains(line, "Action Input:") {
		if foundSections["action"] && midlineActionInputPattern.MatchString(line) {
			return true
		}
	}

	return false
}

// shouldStopParsing checks if we should stop parsing due to hallucinated content.
func shouldStopParsing(line string) bool {
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "[Based on") {
		return true
	}
	if strings.HasPrefix(line, "Observation:") {
		// Don't stop on continuation prompts
		if strings.Contains(line, "Please specify") || strings.Contains(line, "what Action you want to take") {
			return false
		}
		if strings.Contains(line, "Error in reasoning") {
			return false
		}
		return true
	}
	return false
}

// hasMidlineAction checks if text contains a mid-line Action: after sentence boundary.
func hasMidlineAction(text string) bool {
	if text == "" || !strings.Contains(text, "Action:") {
		return false
	}
	return midlineActionPattern.MatchString(text)
}

// hasMidlineFinalAnswer checks if text contains a mid-line Final Answer: after sentence boundary.
func hasMidlineFinalAnswer(text string) bool {
	if text == "" || !strings.Contains(text, "Final Answer:") {
		return false
	}
	return midlineFinalAnswerPattern.MatchString(text)
}

// extractSectionContent safely extracts content from a line with a given prefix.
func extractSectionContent(line, prefix string) string {
	idx := strings.Index(line, prefix)
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(line[idx+len(prefix):])
}

// finalizeSection joins content lines and stores them in the parsed map.
func finalizeSection(parsed map[string]*string, section string, contentLines []string) {
	if section == "" || contentLines == nil {
		return
	}
	content := strings.TrimSpace(strings.Join(contentLines, "\n"))
	// Only overwrite if new content is non-empty or existing is nil
	if content != "" || parsed[section] == nil {
		parsed[section] = &content
	}
}

// setSection sets a section value in the parsed map.
func setSection(parsed map[string]*string, section, value string) {
	parsed[section] = &value
}

// deref safely dereferences a *string, returning "" if nil.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// recoverMissingAction attempts to recover a missing action when Action Input exists
// but Action doesn't. Searches backwards from "Action Input:" for "Action:" or "Action".
func recoverMissingAction(response string) string {
	// Find "Action Input:" position using a case-insensitive regex so byte
	// indices are from the original string (strings.ToLower can shift byte
	// offsets when multi-byte Unicode characters change size on case-fold).
	loc := recoverActionInputPattern.FindStringIndex(response)
	if loc == nil {
		return ""
	}

	textBefore := response[:loc[0]]

	// Try "Action:" first (more specific)
	matches := recoverActionColonPattern.FindAllStringIndex(textBefore, -1)
	if len(matches) > 0 {
		lastMatch := matches[len(matches)-1]
		potential := strings.TrimSpace(textBefore[lastMatch[1]:])
		if validated := validateToolName(potential); validated != "" {
			return validated
		}
	}

	// Try "Action" without colon
	matches = recoverActionWordPattern.FindAllStringIndex(textBefore, -1)
	if len(matches) > 0 {
		lastMatch := matches[len(matches)-1]
		potential := strings.TrimSpace(textBefore[lastMatch[1]:])
		if validated := validateToolName(potential); validated != "" {
			return validated
		}
	}

	return ""
}

// validateToolName checks if text looks like a valid tool name (server.tool format).
func validateToolName(text string) string {
	if text == "" {
		return ""
	}
	// Take only the first line
	firstLine := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if toolNamePattern.MatchString(firstLine) {
		return firstLine
	}
	return ""
}

// GetFormatErrorFeedback returns a specific error message describing what's wrong
// with the response format. This is appended as an observation to help the LLM self-correct.
func GetFormatErrorFeedback(parsed *ParsedReActResponse) string {
	found := parsed.FoundSections

	hasThought := found["thought"]
	hasAction := found["action"]
	hasActionInput := found["action_input"]
	hasFinalAnswer := found["final_answer"]

	var specificError string

	switch {
	case hasAction && !hasActionInput:
		specificError = "FORMAT ERROR: Your response has \"Action:\" but is missing \"Action Input:\".\n" +
			"Every \"Action:\" MUST be followed by \"Action Input:\" (even if empty for no-parameter tools)."
	case hasActionInput && !hasAction:
		specificError = "FORMAT ERROR: Your response has \"Action Input:\" but is missing \"Action:\".\n" +
			"\"Action Input:\" must be preceded by \"Action:\" specifying which tool to call."
	case hasThought && !hasAction && !hasFinalAnswer:
		specificError = "FORMAT ERROR: Your response only contains \"Thought:\".\n" +
			"After reasoning, you MUST either:\n" +
			"- Call a tool with \"Action:\" + \"Action Input:\", OR\n" +
			"- Conclude with \"Final Answer:\""
	case !hasThought && !hasAction && !hasFinalAnswer:
		specificError = "FORMAT ERROR: Could not detect any ReAct sections in your response.\n" +
			"Your response must use the exact format: \"Thought:\", \"Action:\", \"Action Input:\", or \"Final Answer:\""
	default:
		// Use ordered keys for deterministic output (Go map iteration is unordered).
		keys := []string{"thought", "action", "action_input", "final_answer"}
		var foundList, missingList []string
		for _, k := range keys {
			if found[k] {
				foundList = append(foundList, k)
			} else {
				missingList = append(missingList, k)
			}
		}
		specificError = fmt.Sprintf("FORMAT ERROR: Incomplete ReAct format.\nFound: %s\nMissing: %s",
			strings.Join(foundList, ", "), strings.Join(missingList, ", "))
	}

	return specificError + "\n" + GetFormatCorrectionReminder()
}

// GetFormatCorrectionReminder returns a general format reminder.
func GetFormatCorrectionReminder() string {
	return `IMPORTANT: Please follow the exact ReAct format:

1. Use colons: "Thought:", "Action:", "Action Input:", "Final Answer:"
2. Start each section on a NEW LINE (never continue on same line as previous text)
3. Stop after Action Input - the system provides Observations
4. Your response MUST include EITHER tool calling (Action + Action Input) OR Final Answer

Required structure for investigation:
Thought: [your reasoning]
Action: [tool name]
Action Input: [parameters]

For tools with no parameters (keep Action Input empty):
Thought: [your reasoning]
Action: [tool name]
Action Input:

Required structure for conclusion:
Thought: [final reasoning]
Final Answer: [complete analysis]`
}

// FormatObservation formats a tool execution result as a ReAct observation.
func FormatObservation(result *agent.ToolResult) string {
	if result == nil {
		return "Observation: Error - no tool result available"
	}
	if result.IsError {
		return fmt.Sprintf("Observation: Error executing %s: %s", result.Name, result.Content)
	}
	return fmt.Sprintf("Observation: %s", result.Content)
}

// FormatToolErrorObservation formats a tool execution error as an observation.
func FormatToolErrorObservation(err error) string {
	if err == nil {
		return "Observation: Error - Tool execution failed: unknown error"
	}
	return fmt.Sprintf("Observation: Error - Tool execution failed: %s", err.Error())
}

// FormatUnknownToolError formats an error when the LLM requests an unknown tool.
// Includes the list of available tools so the LLM can self-correct.
func FormatUnknownToolError(toolName string, errorMsg string, availableTools []agent.ToolDefinition) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Observation: Error - %s", errorMsg))
	if len(availableTools) > 0 {
		sb.WriteString("\n\nAvailable tools:\n")
		for _, tool := range availableTools {
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", tool.Name, tool.Description))
		}
	} else {
		sb.WriteString("\n\nNo tools are currently available.")
	}
	return sb.String()
}

// FormatErrorObservation formats an LLM call error as an observation for the conversation.
func FormatErrorObservation(err error) string {
	if err == nil {
		return "Observation: Error from previous attempt: unknown error. Please try again."
	}
	return fmt.Sprintf("Observation: Error from previous attempt: %s. Please try again.", err.Error())
}

// ExtractForcedConclusionAnswer extracts the answer from a forced conclusion response.
// If the response has ReAct format, extracts the Final Answer; otherwise uses raw text.
func ExtractForcedConclusionAnswer(parsed *ParsedReActResponse) string {
	if parsed.IsFinalAnswer && parsed.FinalAnswer != "" {
		return parsed.FinalAnswer
	}
	// If the LLM didn't use ReAct format in the forced conclusion,
	// use the full raw thought + any content as the answer
	if parsed.Thought != "" {
		return parsed.Thought
	}
	return ""
}
