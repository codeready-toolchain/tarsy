package mcp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/builtintools"
)

// toolNameRegex validates the "server.tool" format.
// Both server and tool parts must start with a word character and contain
// only word characters and hyphens.
var toolNameRegex = regexp.MustCompile(`^([\w][\w-]*)\.([\w][\w-]*)$`)

// NormalizeBuiltinPlainToolName strips a mistaken provider: prefix when the suffix is
// a known built-in plain tool (e.g. "google:load_skill" → "load_skill").
func NormalizeBuiltinPlainToolName(name string) string {
	if !strings.Contains(name, ":") {
		return name
	}
	last := name[strings.LastIndex(name, ":")+1:]
	if last == "" {
		return name
	}
	if _, ok := builtintools.KindForPlainTool(last); ok {
		return last
	}
	return name
}

// NormalizeToolName converts tool names between controller formats.
// FunctionCalling uses "server__tool" (API name restriction for Gemini/LangChain).
// Normalizes to "server.tool" for routing.
func NormalizeToolName(name string) string {
	// Convert double-underscore to dot (FunctionCalling API format → canonical)
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
		return "", "", fmt.Errorf("%s", describeInvalidToolName(name))
	}
	return matches[1], matches[2], nil
}

func describeInvalidToolName(name string) string {
	if k, ok := builtintools.KindForPlainTool(name); ok {
		return plainToolMisroutedMessage(name, name, k, false)
	}
	if strings.Contains(name, ":") {
		last := name[strings.LastIndex(name, ":")+1:]
		if last != "" {
			if k, ok := builtintools.KindForPlainTool(last); ok {
				return plainToolMisroutedMessage(name, last, k, true)
			}
		}
	}
	return fmt.Sprintf(
		"invalid tool name %q: MCP tools must be server.tool with one dot between server id and tool id (e.g. kubernetes-server.get_pods)",
		name)
}

func plainToolMisroutedMessage(fullName, canonical string, k builtintools.Kind, hadColonPrefix bool) string {
	const mcpExample = "kubernetes-server.get_pods"
	switch k {
	case builtintools.KindOrchestration:
		if hadColonPrefix {
			return fmt.Sprintf(
				"invalid tool name %q: %q is an orchestration tool (dispatch/cancel/list sub-agents) — call it as %q only "+
					"(no provider: or server: prefix). Do not change it to server.tool; that pattern is only for MCP server tools.",
				fullName, canonical, canonical)
		}
		return fmt.Sprintf(
			"invalid tool name %q: this is an orchestration tool (dispatch/cancel/list sub-agents), not an MCP tool — "+
				"use the exact name %q with no prefix. MCP integrations use server.tool with one dot (e.g. %s)",
			fullName, canonical, mcpExample)
	case builtintools.KindSkill:
		if hadColonPrefix {
			return fmt.Sprintf(
				"invalid tool name %q: %q is the on-demand skill loader — call it as %q only "+
					"(no provider: or server: prefix). Do not change it to server.tool; that pattern is only for MCP server tools.",
				fullName, canonical, canonical)
		}
		return fmt.Sprintf(
			"invalid tool name %q: this is the on-demand skill loader, not an MCP tool — "+
				"use the exact name %q with no prefix. MCP integrations use server.tool with one dot (e.g. %s)",
			fullName, canonical, mcpExample)
	case builtintools.KindMemory:
		if hadColonPrefix {
			return fmt.Sprintf(
				"invalid tool name %q: %q is an investigation memory tool — call it as %q only "+
					"(no provider: or server: prefix). Do not change it to server.tool; that pattern is only for MCP server tools.",
				fullName, canonical, canonical)
		}
		return fmt.Sprintf(
			"invalid tool name %q: this is an investigation memory tool (recall or session search), not an MCP tool — "+
				"use the exact name %q with no prefix. MCP integrations use server.tool with one dot (e.g. %s)",
			fullName, canonical, mcpExample)
	default:
		return fmt.Sprintf(
			"invalid tool name %q: MCP tools must be server.tool with one dot between server id and tool id (e.g. %s)",
			fullName, mcpExample)
	}
}
