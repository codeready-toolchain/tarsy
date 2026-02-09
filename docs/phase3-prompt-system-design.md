# Phase 3.3: Prompt System — Detailed Design

**Status**: 🔵 Design Phase  
**Last Updated**: 2026-02-09

## Overview

This document details the Prompt System for the new TARSy. Phase 3.3 replaces the placeholder `buildMessages()` methods in all controllers with a centralized, composable prompt builder framework. The prompt builder owns all prompt text generation — system messages, user messages, instruction composition, chat formatting, forced conclusion prompts, and MCP summarization prompts.

**Phase 3.3 Scope**: Prompt builder package (`pkg/agent/prompt/`), template constants, reusable components (alert section, runbook section, tool formatting), three-tier instruction composition, strategy-specific prompt methods, chat context injection, forced conclusion prompts, MCP result summarization prompts, executive summary prompts.

**Key Design Principles:**
- All prompt building in Go — the prompt builder is a pure Go package with no Python dependency
- Composable components — reusable formatters for alert data, runbook content, tool descriptions, chain context
- Strategy-aware — different prompt shapes for ReAct (text-parsed tools), Native Thinking (structured tools), and Synthesis (tool-less)
- Three-tier instruction composition — general SRE instructions → MCP server instructions → custom agent instructions
- Testable — pure functions and string builders, no side effects, deterministic output
- Chat as a prompt concern — same controllers, different prompt composition (investigation context + chat history injected via prompt builder)

**What This Phase Delivers:**
- `pkg/agent/prompt/` package with builder, components, and template constants
- `PromptBuilder` struct replacing inline `buildMessages()` in all controllers
- Three-tier instruction composition with MCP server registry integration
- Strategy-specific system messages (ReAct with format instructions, Native Thinking without, Synthesis with synthesis task)
- User message building with alert section, runbook section, chain context, and analysis task
- Chat-aware prompt building (investigation context, chat history, current task)
- Forced conclusion prompts (strategy-specific: ReAct format vs plain text)
- MCP result summarization prompts (system + user)
- Executive summary prompts
- ReAct format instructions (comprehensive, with examples)
- Tool description formatting (rich JSON Schema parameter extraction for ReAct)

**What This Phase Does NOT Deliver:**
- MCP client and tool execution (Phase 4)
- Runbook URL fetching / GitHub integration (Phase 6)
- Multi-stage chain orchestration (Phase 5)
- WebSocket streaming (Phase 3.4)

---

## Architecture Overview

### Prompt Builder in the System

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Go Orchestrator                              │
│                                                                     │
│  ┌─────────────────┐         ┌────────────────────┐                │
│  │   Controller     │────────▶│   PromptBuilder    │                │
│  │   (ReAct, NT,   │         │   (pkg/agent/      │                │
│  │    Synthesis)    │         │    prompt/)         │                │
│  └────────┬────────┘         └────────┬───────────┘                │
│           │                           │                             │
│           │                           │ Reads from:                 │
│           │                           ├── MCP Server Registry       │
│           │                           │   (server instructions)     │
│           │                           ├── ExecutionContext           │
│           │                           │   (alert, config, chat)     │
│           │                           └── Template Constants        │
│           │                               (prompt text)             │
│           ▼                                                         │
│  ┌─────────────────┐    ┌──────────────────┐                       │
│  │   LLM Client    │    │  Context          │                       │
│  │   (gRPC→Python) │    │  Formatter        │                       │
│  └─────────────────┘    │  (stage context)  │                       │
│                          └──────────────────┘                       │
└─────────────────────────────────────────────────────────────────────┘
```

### Package Structure

```
pkg/agent/prompt/
├── builder.go          # PromptBuilder struct and main methods
├── components.go       # AlertSection, RunbookSection formatters
├── instructions.go     # Three-tier composition + general instructions
├── templates.go        # All prompt template constants
├── tools.go            # Tool description formatting (ReAct)
├── chat.go             # Chat context formatting
├── builder_test.go     # Tests for builder methods
├── components_test.go  # Tests for components
├── instructions_test.go # Tests for instruction composition
├── tools_test.go       # Tests for tool formatting
└── chat_test.go        # Tests for chat formatting

pkg/agent/context/
├── formatter.go                    # Existing: SimpleContextFormatter (stage context)
├── investigation_formatter.go      # New: FormatInvestigationContext (chat sessions)
└── investigation_formatter_test.go # New: Tests for investigation context formatting
```

---

## PromptBuilder

### Struct and Constructor

The `PromptBuilder` is a concrete struct (not an interface) that receives the MCP server registry at construction time. Controllers receive a `*PromptBuilder` via `ExecutionContext`.

```go
// pkg/agent/prompt/builder.go
package prompt

import (
    "github.com/codeready-toolchain/tarsy/pkg/config"
)

// PromptBuilder builds all prompt text for agent controllers.
// It composes system messages, user messages, instruction hierarchies,
// and strategy-specific formatting. Stateless — all state comes from
// parameters. Thread-safe — no mutable state.
type PromptBuilder struct {
    mcpRegistry *config.MCPServerRegistry
}

// NewPromptBuilder creates a PromptBuilder with access to MCP server configs.
func NewPromptBuilder(mcpRegistry *config.MCPServerRegistry) *PromptBuilder {
    return &PromptBuilder{
        mcpRegistry: mcpRegistry,
    }
}
```

### Integration with ExecutionContext

The `PromptBuilder` is added to `ExecutionContext` so controllers can call it directly:

```go
// pkg/agent/context.go (updated)
type ExecutionContext struct {
    // ... existing fields ...
    
    // Prompt builder (injected by executor)
    PromptBuilder *prompt.PromptBuilder
}
```

The session executor creates one `PromptBuilder` at startup (shared across all agent executions, since it's stateless) and passes it into every `ExecutionContext`.

### Integration with Controllers

Controllers replace their inline `buildMessages()` with calls to the prompt builder:

```go
// In ReActController.Run():
messages := execCtx.PromptBuilder.BuildReActMessages(execCtx, prevStageContext)

// In NativeThinkingController.Run():
messages := execCtx.PromptBuilder.BuildNativeThinkingMessages(execCtx, prevStageContext)

// In SynthesisController.Run():
messages := execCtx.PromptBuilder.BuildSynthesisMessages(execCtx, prevStageContext)
```

**Forced conclusion** (currently `buildForcedConclusionPrompt()` in helpers.go):

```go
// In ReActController — when max iterations reached:
prompt := execCtx.PromptBuilder.BuildForcedConclusionPrompt(iteration, config.IterationStrategyReact)

// In NativeThinkingController:
prompt := execCtx.PromptBuilder.BuildForcedConclusionPrompt(iteration, config.IterationStrategyNativeThinking)
```

---

## Three-Tier Instruction Composition

### Design

Instruction composition follows the same pattern as old TARSy's `_compose_instructions()`:

1. **Tier 1 — General Instructions**: SRE agent role and guidelines, common to all agents
2. **Tier 2 — MCP Server Instructions**: Per-server instructions from the MCP registry (e.g., Kubernetes best practices)
3. **Tier 3 — Custom Instructions**: Agent-specific instructions from configuration (`ResolvedAgentConfig.CustomInstructions`)

The composed instructions form the core of the system message. Strategy-specific additions (ReAct format rules, synthesis task) are layered on top.

### Implementation

```go
// pkg/agent/prompt/instructions.go
package prompt

import (
    "strings"
    
    "github.com/codeready-toolchain/tarsy/pkg/agent"
)

// ComposeInstructions builds the three-tier instruction set for an agent.
func (b *PromptBuilder) ComposeInstructions(execCtx *agent.ExecutionContext) string {
    var sections []string
    
    // Tier 1: General SRE instructions
    sections = append(sections, generalInstructions)
    
    // Tier 2: MCP server instructions (from registry, keyed by server IDs in config)
    for _, serverID := range execCtx.Config.MCPServers {
        serverConfig, err := b.mcpRegistry.Get(serverID)
        if err != nil {
            continue // Skip servers not in registry (logged elsewhere)
        }
        if serverConfig.Instructions != "" {
            sections = append(sections, "## "+serverID+" Instructions\n\n"+serverConfig.Instructions)
        }
    }
    
    // Tier 3: Custom agent instructions
    if execCtx.Config.CustomInstructions != "" {
        sections = append(sections, "## Agent-Specific Instructions\n\n"+execCtx.Config.CustomInstructions)
    }
    
    return strings.Join(sections, "\n\n")
}
```

### General Instructions Content

```go
// pkg/agent/prompt/instructions.go

const generalInstructions = `## General SRE Agent Instructions

You are an expert Site Reliability Engineer (SRE) with deep knowledge of:
- Kubernetes and container orchestration
- Cloud infrastructure and services
- Incident response and troubleshooting
- System monitoring and alerting
- GitOps and deployment practices

Analyze alerts thoroughly and provide actionable insights based on:
1. Alert information and context
2. Associated runbook procedures
3. Real-time system data from available tools

Always be specific, reference actual data, and provide clear next steps.
Focus on root cause analysis and sustainable solutions.`
```

### Chat General Instructions

For chat follow-up sessions, the general instructions are different:

```go
const chatGeneralInstructions = `## Chat Assistant Instructions

You are an expert Site Reliability Engineer (SRE) assistant helping with follow-up questions about a completed alert investigation.

The user has reviewed the investigation results and has follow-up questions. Your role is to:
- Provide clear, actionable answers based on the investigation history
- Use available tools to gather fresh, real-time data when needed
- Reference specific findings from the original investigation when relevant
- Maintain the same professional SRE communication style
- Be concise but thorough in your responses

You have access to the same tools and systems that were used in the original investigation.`
```

A separate `ComposeChatInstructions()` method uses `chatGeneralInstructions` as Tier 1, then applies the same Tier 2 and Tier 3 logic, and appends chat-specific response guidelines:

```go
const chatResponseGuidelines = `## Response Guidelines

1. **Context Awareness**: Reference the investigation history when it provides relevant context
2. **Fresh Data**: Use tools to gather current system state if the question requires up-to-date information
3. **Clarity**: If the question is ambiguous or unclear, ask for clarification in your Final Answer
4. **Specificity**: Always reference actual data and observations, not assumptions
5. **Brevity**: Be concise but complete - users have already read the full investigation`

// ComposeChatInstructions builds the instruction set for chat sessions.
func (b *PromptBuilder) ComposeChatInstructions(execCtx *agent.ExecutionContext) string {
    var sections []string
    
    // Tier 1: Chat-specific general instructions
    sections = append(sections, chatGeneralInstructions)
    
    // Tier 2: MCP server instructions (same logic as investigation)
    for _, serverID := range execCtx.Config.MCPServers {
        serverConfig, err := b.mcpRegistry.Get(serverID)
        if err != nil {
            continue
        }
        if serverConfig.Instructions != "" {
            sections = append(sections, "## "+serverID+" Instructions\n\n"+serverConfig.Instructions)
        }
    }
    
    // Tier 3: Custom agent instructions
    if execCtx.Config.CustomInstructions != "" {
        sections = append(sections, "## Agent-Specific Instructions\n\n"+execCtx.Config.CustomInstructions)
    }
    
    // Chat-specific guidelines
    sections = append(sections, chatResponseGuidelines)
    
    return strings.Join(sections, "\n\n")
}
```

---

## Prompt Components

### Alert Section

Formats alert metadata and alert data for LLM consumption. New TARSy treats `AlertData` as opaque text (it may be JSON, YAML, or plain text — the field is a string, not a structured type). Alert data is passed as-is without any parsing or reformatting, wrapped in HTML comment boundaries for clear delimitation. HTML comment boundaries are the established pattern in TARSy (runbook, stage context, investigation results all use the same approach) and are robust against content collision.

```go
// pkg/agent/prompt/components.go
package prompt

import (
    "strings"
)

// FormatAlertSection builds the alert details section.
// alertType may be empty; alertData is opaque text from the session.
func FormatAlertSection(alertType, alertData string) string {
    var sb strings.Builder
    sb.WriteString("## Alert Details\n\n")
    
    // Alert metadata (if available)
    if alertType != "" {
        sb.WriteString("### Alert Metadata\n")
        sb.WriteString("**Alert Type:** ")
        sb.WriteString(alertType)
        sb.WriteString("\n\n")
    }
    
    // Alert data (opaque text, passed as-is)
    sb.WriteString("### Alert Data\n")
    if alertData == "" {
        sb.WriteString("No additional alert data provided.\n")
        return sb.String()
    }
    
    sb.WriteString("<!-- ALERT_DATA_START -->\n")
    sb.WriteString(alertData)
    sb.WriteString("\n<!-- ALERT_DATA_END -->\n")
    
    return sb.String()
}
```

### Runbook Section

Formats runbook content with HTML comment boundaries for LLM context:

```go
// FormatRunbookSection builds the runbook section.
// runbookContent is the raw runbook text (markdown typically).
func FormatRunbookSection(runbookContent string) string {
    if runbookContent == "" {
        return "## Runbook Content\nNo runbook available.\n"
    }
    
    var sb strings.Builder
    sb.WriteString("## Runbook Content\n")
    sb.WriteString("```markdown\n")
    sb.WriteString("<!-- RUNBOOK START -->\n")
    sb.WriteString(runbookContent)
    sb.WriteString("\n<!-- RUNBOOK END -->\n")
    sb.WriteString("```\n")
    return sb.String()
}
```

### Chain Context (Previous Stage Results)

The existing `ContextFormatter` interface in `pkg/agent/context/formatter.go` handles formatting timeline events from previous stages. The prompt builder receives the pre-formatted string from the controller (which calls `formatter.Format(events)`), and wraps it into a section:

```go
// FormatChainContext wraps pre-formatted previous stage context into a section.
// prevStageContext is the output of ContextFormatter.Format() — already formatted.
func FormatChainContext(prevStageContext string) string {
    if prevStageContext == "" {
        return "## Previous Stage Data\nNo previous stage data is available for this alert. This is the first stage of analysis.\n"
    }
    
    var sb strings.Builder
    sb.WriteString("## Previous Stage Data\n")
    sb.WriteString(prevStageContext)
    sb.WriteString("\n")
    return sb.String()
}
```

---

## Strategy-Specific Prompt Building

### ReAct Messages

The ReAct system message includes composed instructions + ReAct format instructions + task focus. The user message includes alert section, runbook section, chain context, analysis task, and available tools.

```go
// pkg/agent/prompt/builder.go

// BuildReActMessages builds the initial conversation for a ReAct investigation.
func (b *PromptBuilder) BuildReActMessages(
    execCtx *agent.ExecutionContext,
    prevStageContext string,
    tools []agent.ToolDefinition,
) []agent.ConversationMessage {
    isChat := execCtx.ChatContext != nil
    
    // System message
    var systemContent string
    if isChat {
        composed := b.ComposeChatInstructions(execCtx)
        systemContent = composed + "\n\n" + reactFormatInstructions + "\n\n" +
            "Focus on investigation and providing recommendations for human operators to execute."
    } else {
        composed := b.ComposeInstructions(execCtx)
        systemContent = composed + "\n\n" + reactFormatInstructions + "\n\n" +
            "Focus on investigation and providing recommendations for human operators to execute."
    }
    
    messages := []agent.ConversationMessage{
        {Role: "system", Content: systemContent},
    }
    
    // User message
    var userContent string
    if isChat {
        userContent = b.buildChatUserMessage(execCtx, tools)
    } else {
        userContent = b.buildInvestigationUserMessage(execCtx, prevStageContext, tools)
    }
    
    messages = append(messages, agent.ConversationMessage{
        Role:    "user",
        Content: userContent,
    })
    
    return messages
}
```

### Native Thinking Messages

Native Thinking uses the same system message structure but **without** ReAct format instructions (Gemini uses native function calling and internal reasoning). Tools are NOT included in the user message text — they are passed as native function declarations via the gRPC protocol.

```go
// BuildNativeThinkingMessages builds the initial conversation for a native thinking investigation.
func (b *PromptBuilder) BuildNativeThinkingMessages(
    execCtx *agent.ExecutionContext,
    prevStageContext string,
) []agent.ConversationMessage {
    isChat := execCtx.ChatContext != nil
    
    // System message (no ReAct format instructions, no tool descriptions in text)
    var composed string
    if isChat {
        composed = b.ComposeChatInstructions(execCtx)
    } else {
        composed = b.ComposeInstructions(execCtx)
    }
    systemContent := composed + "\n\n" +
        "Focus on investigation and providing recommendations for human operators to execute."
    
    messages := []agent.ConversationMessage{
        {Role: "system", Content: systemContent},
    }
    
    // User message (no tool descriptions — tools are native function declarations)
    var userContent string
    if isChat {
        userContent = b.buildChatUserMessage(execCtx, nil) // nil tools = no tool section
    } else {
        userContent = b.buildInvestigationUserMessage(execCtx, prevStageContext, nil)
    }
    
    messages = append(messages, agent.ConversationMessage{
        Role:    "user",
        Content: userContent,
    })
    
    return messages
}
```

### Synthesis Messages

Synthesis is a tool-less single LLM call that combines results from parallel agents. The system message emphasizes the synthesis task. The user message focuses on previous stage results.

```go
// BuildSynthesisMessages builds the conversation for a synthesis stage.
func (b *PromptBuilder) BuildSynthesisMessages(
    execCtx *agent.ExecutionContext,
    prevStageContext string,
) []agent.ConversationMessage {
    composed := b.ComposeInstructions(execCtx)
    systemContent := composed + "\n\n" +
        "Your task is to synthesize the investigation results from multiple agents " +
        "into a single coherent analysis.\n\n" +
        "Focus on investigation and providing recommendations for human operators to execute."
    
    messages := []agent.ConversationMessage{
        {Role: "system", Content: systemContent},
    }
    
    // User message with synthesis-specific structure
    userContent := b.buildSynthesisUserMessage(execCtx, prevStageContext)
    
    messages = append(messages, agent.ConversationMessage{
        Role:    "user",
        Content: userContent,
    })
    
    return messages
}
```

---

## User Message Building

### Investigation User Message

For standard (non-chat) investigations, the user message composes:

1. Available tools (ReAct only — not included for Native Thinking)
2. Alert section
3. Runbook section  
4. Chain context (previous stage results)
5. Analysis task instructions

```go
// buildInvestigationUserMessage builds the user message for an investigation.
func (b *PromptBuilder) buildInvestigationUserMessage(
    execCtx *agent.ExecutionContext,
    prevStageContext string,
    tools []agent.ToolDefinition,
) string {
    var sb strings.Builder
    
    // Available tools (ReAct only)
    if len(tools) > 0 {
        sb.WriteString("Answer the following question using the available tools.\n\n")
        sb.WriteString("Available tools:\n\n")
        sb.WriteString(FormatToolDescriptions(tools))
        sb.WriteString("\n\n")
    }
    
    // Alert section
    alertType := execCtx.Config.AlertType // from session/chain config
    sb.WriteString(FormatAlertSection(alertType, execCtx.AlertData))
    sb.WriteString("\n")
    
    // Runbook section
    sb.WriteString(FormatRunbookSection(execCtx.RunbookContent))
    sb.WriteString("\n")
    
    // Chain context
    sb.WriteString(FormatChainContext(prevStageContext))
    sb.WriteString("\n")
    
    // Analysis task
    sb.WriteString(analysisTask)
    
    return sb.String()
}
```

### Analysis Task Template

```go
// pkg/agent/prompt/templates.go

const analysisTask = `## Your Task
Use the available tools to investigate this alert and provide:
1. Root cause analysis
2. Current system state assessment
3. Specific remediation steps for human operators
4. Prevention recommendations

Be thorough in your investigation before providing the final answer.`
```

### Synthesis User Message

```go
// buildSynthesisUserMessage builds the user message for synthesis.
func (b *PromptBuilder) buildSynthesisUserMessage(
    execCtx *agent.ExecutionContext,
    prevStageContext string,
) string {
    var sb strings.Builder
    
    sb.WriteString("Synthesize the investigation results and provide recommendations.\n\n")
    
    // Alert section
    sb.WriteString(FormatAlertSection("", execCtx.AlertData))
    sb.WriteString("\n")
    
    // Runbook section
    sb.WriteString(FormatRunbookSection(execCtx.RunbookContent))
    sb.WriteString("\n")
    
    // Previous stage results (the main content for synthesis)
    sb.WriteString(FormatChainContext(prevStageContext))
    sb.WriteString("\n")
    
    // Synthesis instructions
    sb.WriteString(synthesisTask)
    
    return sb.String()
}

const synthesisTask = `## Your Task
Based on the investigation results above, provide a comprehensive synthesis:
1. Combined root cause analysis from all investigations
2. Correlated findings across agents
3. Prioritized remediation steps
4. Overall assessment and recommendations

Focus on correlating findings across the different investigations and providing a unified analysis.`
```

---

## Tool Description Formatting

For ReAct controllers, available tools are formatted as text in the user message. This follows old TARSy's rich parameter formatting with JSON Schema information.

**Note**: Native Thinking controllers do NOT use text-based tool descriptions — tools are passed as native function declarations in the gRPC protocol. The `FormatToolDescriptions()` function is only used by ReAct prompts.

```go
// pkg/agent/prompt/tools.go
package prompt

import (
    "fmt"
    "sort"
    "strings"
    
    "github.com/codeready-toolchain/tarsy/pkg/agent"
)

// FormatToolDescriptions formats tool definitions for ReAct prompt injection.
// Includes rich JSON Schema parameter details for LLM guidance.
func FormatToolDescriptions(tools []agent.ToolDefinition) string {
    if len(tools) == 0 {
        return "No tools available."
    }
    
    var sb strings.Builder
    for i, tool := range tools {
        // Tool name and description
        sb.WriteString(fmt.Sprintf("%d. **%s**: %s\n", i+1, tool.Name, tool.Description))
        
        // Parameters from JSON Schema
        params := extractParameters(tool.InputSchema)
        if len(params) > 0 {
            sb.WriteString("    **Parameters**:\n")
            for _, p := range params {
                sb.WriteString("    - ")
                sb.WriteString(p)
                sb.WriteString("\n")
            }
        } else {
            sb.WriteString("    **Parameters**: None\n")
        }
        
        // Blank line between tools (not after last)
        if i < len(tools)-1 {
            sb.WriteString("\n")
        }
    }
    
    return sb.String()
}

// extractParameters extracts rich parameter info from a JSON Schema.
func extractParameters(schema map[string]any) []string {
    if schema == nil {
        return nil
    }
    
    properties, ok := schema["properties"].(map[string]any)
    if !ok {
        return nil
    }
    
    required := make(map[string]bool)
    if reqList, ok := schema["required"].([]any); ok {
        for _, r := range reqList {
            if s, ok := r.(string); ok {
                required[s] = true
            }
        }
    }
    
    // Sort keys for deterministic output (Q8 decision)
    keys := make([]string, 0, len(properties))
    for k := range properties {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    
    var params []string
    for _, name := range keys {
        propRaw := properties[name]
        prop, ok := propRaw.(map[string]any)
        if !ok {
            continue
        }
        
        var parts []string
        parts = append(parts, name)
        
        // Required/optional + type
        if required[name] {
            parts = append(parts, " (required")
        } else {
            parts = append(parts, " (optional")
        }
        if t, ok := prop["type"].(string); ok {
            parts = append(parts, ", "+t)
        }
        parts[len(parts)-1] += ")"
        
        // Description
        if desc, ok := prop["description"].(string); ok && desc != "" {
            parts = append(parts, ": "+desc)
        }
        
        // Additional schema hints
        var hints []string
        if def, ok := prop["default"]; ok {
            hints = append(hints, fmt.Sprintf("default: %v", def))
        }
        if enum, ok := prop["enum"].([]any); ok {
            vals := make([]string, 0, len(enum))
            for _, v := range enum {
                vals = append(vals, fmt.Sprintf("%q", v))
            }
            hints = append(hints, "choices: ["+strings.Join(vals, ", ")+"]")
        }
        if len(hints) > 0 {
            parts = append(parts, " ["+strings.Join(hints, "; ")+"]")
        }
        
        params = append(params, strings.Join(parts, ""))
    }
    
    return params
}
```

---

## ReAct Format Instructions

The comprehensive ReAct format instructions are a const string in the templates file. This replaces the placeholder `reactFormatInstructions` currently in `react.go`:

```go
// pkg/agent/prompt/templates.go

const reactFormatInstructions = `You are an SRE agent using the ReAct framework to analyze incidents. Reason step by step, act with tools, observe results, and repeat until you identify root cause and resolution steps.

REQUIRED FORMAT:

Question: [the incident question]
Thought: [your step-by-step reasoning]
Action: [tool name from available tools]
Action Input: [parameters as key: value pairs]

⚠️ STOP immediately after Action Input. The system provides Observations.

Continue the cycle. Conclude when you have sufficient information:

Thought: [final reasoning]
Final Answer: [complete structured response]

CRITICAL RULES:
1. Always use colons after headers: "Thought:", "Action:", "Action Input:"
2. Start each section on a NEW LINE (never continue on same line as previous text)
3. Stop after Action Input—never generate fake Observations
4. Parameters: one per line for multiple values, or inline for single value
5. Conclude when you have actionable insights (perfect information not required)

PARAMETER FORMATS:

Multiple parameters:
Action Input: apiVersion: v1
kind: Namespace
name: superman-dev

Single parameter:
Action Input: namespace: default

EXAMPLE CYCLE:

Question: Why is namespace 'superman-dev' stuck in terminating state?

Thought: I need to check the namespace status first to identify any blocking resources or finalizers.

Action: kubernetes-server.resources_get
Action Input: apiVersion: v1
kind: Namespace
name: superman-dev

[System provides: Observation: {"status": {"phase": "Terminating", "finalizers": ["kubernetes"]}}]

Thought: A finalizer is blocking deletion. I should check for any remaining resources in the namespace.

Action: kubernetes-server.resources_list
Action Input: apiVersion: v1
kind: Pod
namespace: superman-dev

[System provides: Observation: No pods found]

Thought: No pods remain, but the finalizer persists. This is an orphaned finalizer that needs manual removal.

Final Answer:
**Root Cause:** Orphaned 'kubernetes' finalizer blocking namespace deletion after all resources were cleaned up.

**Resolution Steps:**
1. Remove the finalizer: ` + "`" + `kubectl patch namespace superman-dev -p '{"spec":{"finalizers":null}}' --type=merge` + "`" + `
2. Verify deletion: ` + "`" + `kubectl get namespace superman-dev` + "`" + `
3. If still stuck, check for remaining resources: ` + "`" + `kubectl api-resources --verbs=list --namespaced -o name | xargs -n 1 kubectl get -n superman-dev` + "`" + `

**Preventive Measures:** Ensure cleanup scripts remove finalizers when deleting namespaces programmatically.`
```

---

## Forced Conclusion Prompts

When a controller reaches `maxIterations`, it sends a forced conclusion prompt. The prompt is strategy-specific:

```go
// pkg/agent/prompt/builder.go

// BuildForcedConclusionPrompt returns a prompt to force an LLM conclusion
// at the iteration limit. The format depends on the iteration strategy.
func (b *PromptBuilder) BuildForcedConclusionPrompt(iteration int, strategy config.IterationStrategy) string {
    var formatInstructions string
    switch strategy {
    case config.IterationStrategyReact:
        formatInstructions = reactForcedConclusionFormat
    case config.IterationStrategyNativeThinking:
        formatInstructions = nativeThinkingForcedConclusionFormat
    default:
        formatInstructions = nativeThinkingForcedConclusionFormat
    }
    
    return fmt.Sprintf(forcedConclusionTemplate, iteration, formatInstructions)
}
```

Templates:

```go
// pkg/agent/prompt/templates.go

const forcedConclusionTemplate = `You have reached the investigation iteration limit (%d iterations).

Please conclude your investigation by answering the original question based on what you've discovered.

**Conclusion guidance:**
- Use the data and observations you've already gathered
- Perfect information is not required - provide actionable insights from available findings
- If gaps remain, clearly state what you couldn't determine and why
- Focus on practical next steps based on current knowledge

%s`

const reactForcedConclusionFormat = `**CRITICAL:** You MUST format your response using the ReAct format:

Thought: [your final reasoning about what you've discovered]
Final Answer: [your complete structured conclusion]

The "Final Answer:" marker is required for proper parsing. Begin your conclusion now.`

const nativeThinkingForcedConclusionFormat = `Provide a clear, structured conclusion that directly addresses the investigation question.`
```

---

## Chat Context Formatting

### Overview

Chat is implemented as "same controllers, different prompts." When `ExecutionContext.ChatContext` is non-nil, the prompt builder:

1. Uses chat-specific general instructions (Tier 1)
2. Includes the investigation context (formatted history of the original investigation)
3. Includes chat history (previous follow-up exchanges)
4. Ends with the current user question as the task

### Chat User Message Building

```go
// pkg/agent/prompt/chat.go
package prompt

import (
    "fmt"
    "strings"
    
    "github.com/codeready-toolchain/tarsy/pkg/agent"
)

// buildChatUserMessage builds the user message for a chat follow-up session.
func (b *PromptBuilder) buildChatUserMessage(
    execCtx *agent.ExecutionContext,
    tools []agent.ToolDefinition,
) string {
    chat := execCtx.ChatContext
    
    var sb strings.Builder
    
    // Available tools (ReAct only)
    if len(tools) > 0 {
        sb.WriteString("Answer the following question using the available tools.\n\n")
        sb.WriteString("Available tools:\n\n")
        sb.WriteString(FormatToolDescriptions(tools))
        sb.WriteString("\n\n")
    }
    
    // Investigation context (pre-formatted by executor/service before execution)
    sb.WriteString(chat.InvestigationContext)
    
    // Chat history (previous exchanges, if any)
    if len(chat.ChatHistory) > 0 {
        sb.WriteString("\n")
        sb.WriteString(FormatChatHistory(chat.ChatHistory))
    }
    
    // Current task
    sb.WriteString(fmt.Sprintf(`
%s
🎯 CURRENT TASK
%s

**Question:** %s

**Your Task:**
Answer the user's question based on the investigation context above.
- Reference investigation history when relevant
- Use tools to get fresh data if needed
- Provide clear, actionable responses

Begin your response:
`, separator, separator, chat.UserQuestion))
    
    return sb.String()
}

const separator = "═══════════════════════════════════════════════════════════════════════════════"
```

### Investigation Context Formatting

The investigation context is formatted from timeline events and LLM interactions of the original investigation session. This is done by the executor/service layer before creating the `ChatContext`, not by the prompt builder itself. The prompt builder receives it as a pre-formatted string.

This function lives in `pkg/agent/context/investigation_formatter.go` (alongside the existing `SimpleContextFormatter` in `formatter.go`) because it transforms timeline events into text — the same responsibility as the context package. This avoids creating a prompt → Ent dependency.

The formatting logic:

```go
// FormatInvestigationContext formats timeline events from the original
// investigation into a readable context for chat sessions.
// This is called by the executor/service layer, not by the prompt builder.
func FormatInvestigationContext(events []*ent.TimelineEvent) string {
    var sb strings.Builder
    sb.WriteString(separator + "\n")
    sb.WriteString("📋 INVESTIGATION HISTORY\n")
    sb.WriteString(separator + "\n\n")
    sb.WriteString("# Original Investigation\n\n")
    
    for _, event := range events {
        switch event.EventType {
        case "llm_thinking":
            sb.WriteString("**Internal Reasoning:**\n\n")
            sb.WriteString(event.Content)
            sb.WriteString("\n\n")
        case "llm_response":
            sb.WriteString("**Agent Response:**\n\n")
            sb.WriteString(event.Content)
            sb.WriteString("\n\n")
        case "tool_call":
            sb.WriteString("**Tool Call:** ")
            sb.WriteString(event.Content)
            sb.WriteString("\n\n")
        case "tool_result":
            sb.WriteString("**Observation:**\n\n")
            sb.WriteString(event.Content)
            sb.WriteString("\n\n")
        case "final_analysis":
            sb.WriteString("**Final Analysis:**\n\n")
            sb.WriteString(event.Content)
            sb.WriteString("\n\n")
        default:
            sb.WriteString("**" + strings.ReplaceAll(event.EventType, "_", " ") + ":**\n\n")
            sb.WriteString(event.Content)
            sb.WriteString("\n\n")
        }
    }
    
    return sb.String()
}
```

### Chat History Formatting

Previous chat exchanges are formatted with clear section markers:

```go
// FormatChatHistory formats previous chat exchanges for inclusion in the prompt.
func FormatChatHistory(exchanges []agent.ChatExchange) string {
    if len(exchanges) == 0 {
        return ""
    }
    
    var sb strings.Builder
    sb.WriteString("\n")
    sb.WriteString(separator + "\n")
    sb.WriteString(fmt.Sprintf("💬 CHAT HISTORY (%d previous exchange%s)\n",
        len(exchanges), pluralS(len(exchanges))))
    sb.WriteString(separator + "\n\n")
    
    for i, exchange := range exchanges {
        sb.WriteString(fmt.Sprintf("## Exchange %d\n\n", i+1))
        sb.WriteString("**USER:**\n")
        sb.WriteString(exchange.UserQuestion)
        sb.WriteString("\n\n")
        
        // Format the conversation messages (assistant responses, observations)
        for _, msg := range exchange.Messages {
            if msg.Role == "assistant" {
                sb.WriteString("**ASSISTANT:**\n")
                sb.WriteString(msg.Content)
                sb.WriteString("\n\n")
            } else if msg.Role == "user" {
                // Observation messages (tool results)
                sb.WriteString("**Observation:**\n\n")
                sb.WriteString(msg.Content)
                sb.WriteString("\n\n")
            }
        }
    }
    
    return sb.String()
}
```

---

## MCP Result Summarization Prompts

**Note:** These prompt templates are created in Phase 3.3 but will not be called until Phase 4 (MCP Integration) implements the tool result summarization flow.

When an MCP tool result exceeds the configured size threshold (`SummarizationConfig.SizeThresholdTokens`), the system makes a separate LLM call to summarize the result before injecting it as an observation. The prompt builder provides both the system and user prompts for this summarization call.

```go
// pkg/agent/prompt/builder.go

// BuildMCPSummarizationSystemPrompt builds the system prompt for MCP result summarization.
func (b *PromptBuilder) BuildMCPSummarizationSystemPrompt(serverName, toolName string, maxSummaryTokens int) string {
    return fmt.Sprintf(mcpSummarizationSystemTemplate, serverName, toolName, maxSummaryTokens)
}

// BuildMCPSummarizationUserPrompt builds the user prompt for MCP result summarization.
func (b *PromptBuilder) BuildMCPSummarizationUserPrompt(conversationContext, serverName, toolName, resultText string) string {
    return fmt.Sprintf(mcpSummarizationUserTemplate, conversationContext, serverName, toolName, resultText)
}
```

Templates:

```go
// pkg/agent/prompt/templates.go

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
```

---

## Executive Summary Prompts

**Note:** These prompt templates are created in Phase 3.3 but will not be called until Phase 5 (Session Completion → Executive summary generation).

After an investigation completes, the system generates a brief executive summary for alert notifications:

```go
// BuildExecutiveSummarySystemPrompt returns the system prompt for executive summary generation.
func (b *PromptBuilder) BuildExecutiveSummarySystemPrompt() string {
    return executiveSummarySystemPrompt
}

// BuildExecutiveSummaryUserPrompt builds the user prompt for generating an executive summary.
func (b *PromptBuilder) BuildExecutiveSummaryUserPrompt(finalAnalysis string) string {
    return fmt.Sprintf(executiveSummaryUserTemplate, finalAnalysis)
}
```

Templates:

```go
const executiveSummarySystemPrompt = `You are an expert Site Reliability Engineer assistant that creates concise 1-4 line executive summaries of incident analyses for alert notifications. Focus on clarity, brevity, and actionable information.`

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
```

---

## Required Changes to Existing Code

### ExecutionContext Updates

Add new fields to `ExecutionContext` and update `ChatContext` type:

```go
// pkg/agent/context.go

type ExecutionContext struct {
    // ... existing fields ...
    
    // Alert type (from session/chain config)
    AlertType string
    
    // Runbook content (fetched by executor, passed as text)
    RunbookContent string
    
    // Prompt builder (injected by executor)
    PromptBuilder *prompt.PromptBuilder
}

// ChatExchange groups a user question with its complete conversation.
type ChatExchange struct {
    UserQuestion string
    Messages     []ConversationMessage
}

// ChatContext carries chat-specific data for controllers.
// Updated: ChatHistory uses ChatExchange for clean exchange boundaries.
type ChatContext struct {
    UserQuestion        string
    InvestigationContext string
    ChatHistory         []ChatExchange
}
```

**Note**: `AlertType` and `RunbookContent` are populated by the session executor from the `AlertSession` and chain configuration. `RunbookContent` is plain text in Phase 3.3 — URL-based runbook fetching comes in Phase 6.

### Controller Changes

Each controller's `buildMessages()` is replaced with a call to the corresponding `PromptBuilder` method. The private `buildMessages()` methods and the `reactFormatInstructions` const in `react.go` are removed.

**ReActController** — before:
```go
messages := c.buildMessages(execCtx, prevStageContext)
```

**ReActController** — after:
```go
tools, err := execCtx.ToolExecutor.ListTools(ctx)
if err != nil {
    return nil, fmt.Errorf("failed to list tools: %w", err)
}
messages := execCtx.PromptBuilder.BuildReActMessages(execCtx, prevStageContext, tools)
```

**NativeThinkingController** — before:
```go
messages := c.buildMessages(execCtx, prevStageContext)
```

**NativeThinkingController** — after:
```go
messages := execCtx.PromptBuilder.BuildNativeThinkingMessages(execCtx, prevStageContext)
```

**SynthesisController** — before:
```go
messages := c.buildMessages(execCtx, prevStageContext)
```

**SynthesisController** — after:
```go
messages := execCtx.PromptBuilder.BuildSynthesisMessages(execCtx, prevStageContext)
```

**Forced conclusion** — before (in `helpers.go`):
```go
prompt := buildForcedConclusionPrompt(iteration)
```

**Forced conclusion** — after:
```go
conclusionPrompt := execCtx.PromptBuilder.BuildForcedConclusionPrompt(iteration, execCtx.Config.IterationStrategy)
```

### helpers.go Cleanup

Remove from `pkg/agent/controller/helpers.go`:
- `buildForcedConclusionPrompt()` — moved to `pkg/agent/prompt/builder.go`

### Backward Compatibility

The `SingleCallController` (Phase 3.1's basic controller) also needs updating to use the prompt builder. Since it's primarily used for testing, it can use `BuildNativeThinkingMessages()` (simplest format).

---

## Template Strategy: String Constants (Not Go text/template)

All prompt templates use Go string constants with `fmt.Sprintf` or `strings.Builder` for composition. This is a deliberate choice:

- **Simplicity**: No template parsing, no template execution errors at runtime
- **Type safety**: `fmt.Sprintf` with `%s` / `%d` provides compile-time format checking
- **Performance**: String constants are zero-allocation; `strings.Builder` is efficient
- **Testability**: Pure string comparison in tests
- **Go-idiomatic**: Standard library patterns, no external dependencies

The old TARSy used LangChain's `PromptTemplate` which is essentially `.format()` on Python strings — the Go equivalent is `fmt.Sprintf`, which is simpler and doesn't require a template engine.

---

## Testing Strategy

### Unit Tests

Each file in `pkg/agent/prompt/` has a corresponding `_test.go` file:

- **`builder_test.go`**: Tests for `BuildReActMessages`, `BuildNativeThinkingMessages`, `BuildSynthesisMessages`, `BuildForcedConclusionPrompt`
  - Verify correct message count (system + user)
  - Verify system message contains composed instructions
  - Verify ReAct system message contains format instructions, NT does not
  - Verify chat vs non-chat message composition
  
- **`components_test.go`**: Tests for `FormatAlertSection`, `FormatRunbookSection`, `FormatChainContext`
  - Verify alert section with/without alert type
  - Verify alert data wrapped in HTML comment boundaries
  - Verify empty alert data handling
  - Verify empty runbook handling
  - Verify empty chain context
  
- **`instructions_test.go`**: Tests for `ComposeInstructions`, `ComposeChatInstructions`
  - Verify three-tier composition ordering
  - Verify MCP server instructions inclusion
  - Verify custom instructions inclusion
  - Verify chat instructions use different general instructions
  
- **`tools_test.go`**: Tests for `FormatToolDescriptions`, `extractParameters`
  - Verify tool formatting with various schema types
  - Verify required/optional parameter annotations
  - Verify enum and default value rendering
  
- **`chat_test.go`**: Tests for `FormatChatHistory`, `buildChatUserMessage`
  - Verify chat history formatting with multiple ChatExchange entries
  - Verify section separators and markers
  - Verify chat user message composition (tools + context + history + task)

**In `pkg/agent/context/`:**

- **`investigation_formatter_test.go`**: Tests for `FormatInvestigationContext`
  - Verify investigation context with various event types (thinking, response, tool_call, tool_result, final_analysis)
  - Verify empty events handling
  - Verify section headers and markers

### Integration Tests

Controller integration tests (existing) verify that the prompt builder produces messages that the LLM client can consume via gRPC. These tests use the stub `ToolExecutor` and a mock `LLMClient`.

---

## Implementation Order

1. **Create `pkg/agent/prompt/` package** — `templates.go` (all constants), `instructions.go` (three-tier composition)
2. **Components** — `components.go` (alert, runbook, chain context formatters)
3. **Tool formatting** — `tools.go` (rich parameter extraction)
4. **Builder core** — `builder.go` (struct, constructor, `BuildReActMessages`, `BuildNativeThinkingMessages`, `BuildSynthesisMessages`)
5. **Forced conclusion** — add to `builder.go`
6. **Chat** — `chat.go` (chat user message, chat history formatting) + `pkg/agent/context/investigation_formatter.go` (investigation context formatting)
7. **MCP summarization + executive summary** — add to `builder.go` and `templates.go`
8. **ExecutionContext updates** — add `PromptBuilder`, `AlertType`, `RunbookContent` fields
9. **Controller migration** — replace `buildMessages()` in all controllers, remove old code
10. **Tests** — unit tests for each file, update integration tests

---

## Summary of Files Changed/Created

| File | Action | Description |
|---|---|---|
| `pkg/agent/prompt/builder.go` | **Create** | PromptBuilder struct, main methods |
| `pkg/agent/prompt/templates.go` | **Create** | All prompt template constants |
| `pkg/agent/prompt/instructions.go` | **Create** | Three-tier composition, general instructions |
| `pkg/agent/prompt/components.go` | **Create** | Alert, runbook, chain context formatters |
| `pkg/agent/prompt/tools.go` | **Create** | Tool description formatting |
| `pkg/agent/prompt/chat.go` | **Create** | Chat-specific formatting |
| `pkg/agent/context/investigation_formatter.go` | **Create** | FormatInvestigationContext for chat sessions |
| `pkg/agent/prompt/*_test.go` | **Create** | Unit tests for prompt package |
| `pkg/agent/context/investigation_formatter_test.go` | **Create** | Tests for investigation context formatting |
| `pkg/agent/context.go` | **Modify** | Add `PromptBuilder`, `AlertType`, `RunbookContent`, `ChatExchange` type, update `ChatContext` |
| `pkg/agent/controller/react.go` | **Modify** | Replace `buildMessages()`, remove `reactFormatInstructions` |
| `pkg/agent/controller/native_thinking.go` | **Modify** | Replace `buildMessages()` |
| `pkg/agent/controller/synthesis.go` | **Modify** | Replace `buildMessages()` |
| `pkg/agent/controller/helpers.go` | **Modify** | Remove `buildForcedConclusionPrompt()` |
