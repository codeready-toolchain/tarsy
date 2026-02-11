package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/masking"
)

// Compile-time check that ToolExecutor implements agent.ToolExecutor.
var _ agent.ToolExecutor = (*ToolExecutor)(nil)

// ToolExecutor implements agent.ToolExecutor backed by real MCP servers.
// Created per-session by ClientFactory.
type ToolExecutor struct {
	client   *Client
	registry *config.MCPServerRegistry

	// Resolved list of server IDs this executor can access.
	serverIDs []string

	// Optional tool filter per server (from MCP selection override).
	// nil means all tools for that server are available.
	toolFilter map[string][]string // serverID → allowed tool names (nil = all)

	// Optional masking service for redacting sensitive data in tool results.
	// nil means no masking is applied.
	maskingService *masking.Service
}

// NewToolExecutor creates a new executor for the given servers.
// maskingService may be nil (masking disabled).
func NewToolExecutor(
	client *Client,
	registry *config.MCPServerRegistry,
	serverIDs []string,
	toolFilter map[string][]string,
	maskingService *masking.Service,
) *ToolExecutor {
	return &ToolExecutor{
		client:         client,
		registry:       registry,
		serverIDs:      serverIDs,
		toolFilter:     toolFilter,
		maskingService: maskingService,
	}
}

// Execute runs a tool call via MCP.
//
// Flow:
//  1. Normalize tool name (server__tool → server.tool for NativeThinking)
//  2. Split and validate server.tool name
//  3. Check server is in allowed serverIDs
//  4. Check tool is in allowed tools (if filter set)
//  5. Parse Arguments string into map[string]any
//  6. Call Client.CallTool(ctx, serverID, toolName, params)
//  7. Convert MCP result to ToolResult
//  8. Apply data masking (if masking service configured)
//  9. Return ToolResult (summarization is handled at the controller level)
func (e *ToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	// Step 1: Normalize name
	name := NormalizeToolName(call.Name)

	// Step 2-4: Route and validate
	serverID, toolName, err := e.resolveToolCall(name)
	if err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: err.Error(),
			IsError: true,
		}, nil // Return error as content, not as Go error (MCP convention)
	}

	// Step 5: Parse arguments
	params, err := ParseActionInput(call.Arguments)
	if err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("Failed to parse tool arguments: %s", err),
			IsError: true,
		}, nil
	}

	// Step 6: Execute via MCP
	result, err := e.client.CallTool(ctx, serverID, toolName, params)
	if err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("MCP tool execution failed: %s", err),
			IsError: true,
		}, nil
	}

	// Step 7: Convert to ToolResult
	content := extractTextContent(result)

	// Step 8: Apply data masking
	if e.maskingService != nil {
		content = e.maskingService.MaskToolResult(content, serverID)
	}

	// Note: Summarization is performed at the controller level (not here),
	// because it requires LLM access, conversation context, and event publishing
	// which are not available to ToolExecutor. See pkg/agent/controller/summarize.go.

	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: content,
		IsError: result.IsError,
	}, nil
}

// ListTools returns all available tools from configured MCP servers.
// Tools are returned with server-prefixed names (e.g., "kubernetes-server.get_pods").
func (e *ToolExecutor) ListTools(ctx context.Context) ([]agent.ToolDefinition, error) {
	var allTools []agent.ToolDefinition

	for _, serverID := range e.serverIDs {
		tools, err := e.client.ListTools(ctx, serverID)
		if err != nil {
			// Log error but continue — partial tools are better than none
			slog.Warn("Failed to list tools from MCP server",
				"server", serverID, "error", err)
			continue
		}

		for _, tool := range tools {
			// Apply tool filter if set
			if filter, ok := e.toolFilter[serverID]; ok && len(filter) > 0 {
				if !slices.Contains(filter, tool.Name) {
					continue
				}
			}

			allTools = append(allTools, agent.ToolDefinition{
				Name:             fmt.Sprintf("%s.%s", serverID, tool.Name),
				Description:      tool.Description,
				ParametersSchema: marshalSchema(tool.InputSchema),
			})
		}
	}

	if len(allTools) == 0 {
		return nil, nil // Consistent with StubToolExecutor contract
	}
	return allTools, nil
}

// Close releases resources (MCP transports, subprocesses).
func (e *ToolExecutor) Close() error {
	if e.client != nil {
		return e.client.Close()
	}
	return nil
}

// resolveToolCall validates a tool call against the executor's configuration.
func (e *ToolExecutor) resolveToolCall(name string) (serverID, toolName string, err error) {
	serverID, toolName, err = SplitToolName(name)
	if err != nil {
		return "", "", err
	}

	// Check server is in allowed list
	if !slices.Contains(e.serverIDs, serverID) {
		return "", "", fmt.Errorf(
			"MCP server %q is not available for this execution. "+
				"Available servers: %s", serverID, strings.Join(e.serverIDs, ", "))
	}

	// Check tool filter (per-alert MCP selection)
	if filter, ok := e.toolFilter[serverID]; ok && len(filter) > 0 {
		if !slices.Contains(filter, toolName) {
			return "", "", fmt.Errorf(
				"tool %q is not available on server %q. "+
					"Available tools: %s", toolName, serverID, strings.Join(filter, ", "))
		}
	}

	return serverID, toolName, nil
}

// extractTextContent extracts text from MCP CallToolResult.
// Concatenates all TextContent items. Non-text content (images, embedded
// resources) is logged at debug level and skipped.
func extractTextContent(result *mcpsdk.CallToolResult) string {
	var parts []string
	for _, c := range result.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			parts = append(parts, tc.Text)
		} else {
			slog.Debug("MCP tool returned non-text content, skipping",
				"content_type", fmt.Sprintf("%T", c))
		}
	}
	return strings.Join(parts, "\n")
}

// marshalSchema serializes a tool's InputSchema to a JSON string.
func marshalSchema(schema any) string {
	if schema == nil {
		return ""
	}
	data, err := json.Marshal(schema)
	if err != nil {
		slog.Debug("Failed to marshal tool input schema", "error", err)
		return ""
	}
	return string(data)
}
