package mcp

import (
	"fmt"
	"regexp"
	"strings"
)

// toolNameRegex validates the "server.tool" format.
// Both server and tool parts must start with a word character and contain
// only word characters and hyphens.
var toolNameRegex = regexp.MustCompile(`^([\w][\w-]*)\.([\w][\w-]*)$`)

// NormalizeToolName converts tool names between controller formats.
// NativeThinking uses "server__tool" (Gemini function name restriction).
// ReAct uses "server.tool" (text-based).
// Normalizes both to "server.tool" for routing.
func NormalizeToolName(name string) string {
	// Convert double-underscore to dot (NativeThinking → canonical)
	if strings.Contains(name, "__") && !strings.Contains(name, ".") {
		return strings.Replace(name, "__", ".", 1)
	}
	return name
}

// SplitToolName splits "server.tool" into (serverID, toolName, error).
// Validates format with strict regex: server and tool parts must be
// word characters and hyphens, non-empty.
func SplitToolName(name string) (serverID, toolName string, err error) {
	matches := toolNameRegex.FindStringSubmatch(name)
	if matches == nil {
		return "", "", fmt.Errorf(
			"invalid tool name %q: must be in 'server.tool' format "+
				"(e.g., 'kubernetes-server.get_pods')", name)
	}
	return matches[1], matches[2], nil
}
