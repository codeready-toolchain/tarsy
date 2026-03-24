package controller

import (
	"fmt"
	"regexp"
	"strings"
)

// actionsTakenRegex matches a bare YES or NO on the last line (case-insensitive).
var actionsTakenRegex = regexp.MustCompile(`(?i)^\s*(YES|NO)\s*$`)

// ExtractActionsTaken parses the action agent's response to extract the
// YES/NO marker from the last line and return the cleaned analysis
// (everything before the marker line).
func ExtractActionsTaken(text string) (taken bool, cleanedAnalysis string, err error) {
	text = strings.TrimRight(text, "\n\r ")
	if text == "" {
		return false, "", fmt.Errorf("empty response text")
	}

	lastNewline := strings.LastIndex(text, "\n")
	var lastLine string
	if lastNewline == -1 {
		lastLine = text
	} else {
		lastLine = text[lastNewline+1:]
	}

	match := actionsTakenRegex.FindStringSubmatch(lastLine)
	if match == nil {
		return false, "", fmt.Errorf("no YES/NO marker found on last line: %q", lastLine)
	}

	taken = strings.EqualFold(match[1], "YES")

	if lastNewline == -1 {
		cleanedAnalysis = ""
	} else {
		cleanedAnalysis = text[:lastNewline]
	}

	return taken, cleanedAnalysis, nil
}
