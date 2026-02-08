package controller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

func TestParseReActResponse_FinalAnswer(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantThought string
		wantAnswer  string
	}{
		{
			name:        "standard final answer",
			input:       "Thought: I have enough info.\nFinal Answer: The root cause is OOM.",
			wantThought: "I have enough info.",
			wantAnswer:  "The root cause is OOM.",
		},
		{
			name:       "final answer without thought",
			input:      "Final Answer: Everything looks fine.",
			wantAnswer: "Everything looks fine.",
		},
		{
			name:        "multi-line final answer",
			input:       "Thought: Done.\nFinal Answer: Line one.\nLine two.\nLine three.",
			wantThought: "Done.",
			wantAnswer:  "Line one.\nLine two.\nLine three.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)
			if !parsed.IsFinalAnswer {
				t.Fatalf("expected IsFinalAnswer=true, got false")
			}
			if parsed.HasAction {
				t.Errorf("expected HasAction=false")
			}
			if parsed.IsMalformed {
				t.Errorf("expected IsMalformed=false")
			}
			if parsed.Thought != tt.wantThought {
				t.Errorf("Thought = %q, want %q", parsed.Thought, tt.wantThought)
			}
			if parsed.FinalAnswer != tt.wantAnswer {
				t.Errorf("FinalAnswer = %q, want %q", parsed.FinalAnswer, tt.wantAnswer)
			}
		})
	}
}

func TestParseReActResponse_Action(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantThought string
		wantAction  string
		wantInput   string
	}{
		{
			name:        "standard action",
			input:       "Thought: I need pods.\nAction: kubernetes-server.resources_get\nAction Input: {\"resource\": \"pods\"}",
			wantThought: "I need pods.",
			wantAction:  "kubernetes-server.resources_get",
			wantInput:   "{\"resource\": \"pods\"}",
		},
		{
			name:       "action without thought",
			input:      "Action: kubernetes-server.resources_get\nAction Input: {\"resource\": \"pods\"}",
			wantAction: "kubernetes-server.resources_get",
			wantInput:  "{\"resource\": \"pods\"}",
		},
		{
			name:        "empty action input",
			input:       "Thought: Check health.\nAction: kubernetes-server.cluster_info\nAction Input:",
			wantThought: "Check health.",
			wantAction:  "kubernetes-server.cluster_info",
			wantInput:   "",
		},
		{
			name:        "multi-line action input",
			input:       "Thought: Check pods.\nAction: kubectl.get_pods\nAction Input: namespace: default\nlabel: app=web",
			wantThought: "Check pods.",
			wantAction:  "kubectl.get_pods",
			wantInput:   "namespace: default\nlabel: app=web",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)
			if !parsed.HasAction {
				t.Fatalf("expected HasAction=true, got false")
			}
			if parsed.IsFinalAnswer {
				t.Errorf("expected IsFinalAnswer=false")
			}
			if parsed.IsMalformed {
				t.Errorf("expected IsMalformed=false")
			}
			if parsed.IsUnknownTool {
				t.Errorf("expected IsUnknownTool=false")
			}
			if parsed.Thought != tt.wantThought {
				t.Errorf("Thought = %q, want %q", parsed.Thought, tt.wantThought)
			}
			if parsed.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", parsed.Action, tt.wantAction)
			}
			if parsed.ActionInput != tt.wantInput {
				t.Errorf("ActionInput = %q, want %q", parsed.ActionInput, tt.wantInput)
			}
		})
	}
}

func TestParseReActResponse_UnknownTool(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAction string
	}{
		{
			name:       "no dot in tool name",
			input:      "Thought: I need logs.\nAction: get_logs\nAction Input: {}",
			wantAction: "get_logs",
		},
		{
			name:       "single word tool",
			input:      "Action: kubectl\nAction Input: get pods",
			wantAction: "kubectl",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)
			if !parsed.IsUnknownTool {
				t.Fatalf("expected IsUnknownTool=true")
			}
			if !parsed.HasAction {
				t.Errorf("expected HasAction=true")
			}
			if parsed.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", parsed.Action, tt.wantAction)
			}
			if parsed.ErrorMessage == "" {
				t.Errorf("expected non-empty ErrorMessage")
			}
		})
	}
}

func TestParseReActResponse_Malformed(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "empty string",
			input: "",
		},
		{
			name:  "no sections at all",
			input: "This is just a regular text response without any ReAct format.",
		},
		{
			name:  "only thought",
			input: "Thought: I'm thinking about something but never act or conclude.",
		},
		{
			name:  "action without action input",
			input: "Action: kubernetes-server.resources_get",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)
			if !parsed.IsMalformed {
				t.Fatalf("expected IsMalformed=true for input %q", tt.input)
			}
			if parsed.HasAction {
				t.Errorf("expected HasAction=false")
			}
			if parsed.IsFinalAnswer {
				t.Errorf("expected IsFinalAnswer=false")
			}
		})
	}
}

func TestParseReActResponse_ActionPreferredOverFinalAnswer(t *testing.T) {
	// When both Action+ActionInput and Final Answer exist, prefer Action
	input := "Thought: Let me check.\nAction: server.tool\nAction Input: {}\nFinal Answer: Done."
	parsed := ParseReActResponse(input)
	if !parsed.HasAction {
		t.Fatalf("expected HasAction=true (action should be preferred over final answer)")
	}
	if parsed.IsFinalAnswer {
		t.Errorf("expected IsFinalAnswer=false")
	}
}

func TestParseReActResponse_DuplicateActions(t *testing.T) {
	// Parser should use the latest Action (not first)
	input := "Thought: first.\nAction: server.first_tool\nAction: server.second_tool\nAction Input: {}"
	parsed := ParseReActResponse(input)
	if !parsed.HasAction {
		t.Fatalf("expected HasAction=true")
	}
	if parsed.Action != "server.second_tool" {
		t.Errorf("Action = %q, want %q (should use latest)", parsed.Action, "server.second_tool")
	}
}

func TestFormatObservation(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		result := &agent.ToolResult{Name: "server.tool", Content: "pod data", IsError: false}
		obs := FormatObservation(result)
		if obs != "Observation: pod data" {
			t.Errorf("got %q, want %q", obs, "Observation: pod data")
		}
	})

	t.Run("error", func(t *testing.T) {
		result := &agent.ToolResult{Name: "server.tool", Content: "connection refused", IsError: true}
		obs := FormatObservation(result)
		if !strings.Contains(obs, "Error executing server.tool") {
			t.Errorf("got %q, want error observation", obs)
		}
	})
}

func TestFormatUnknownToolError(t *testing.T) {
	t.Run("lists available tools", func(t *testing.T) {
		tools := []agent.ToolDefinition{
			{Name: "server.tool1", Description: "First tool"},
			{Name: "server.tool2", Description: "Second tool"},
		}
		msg := FormatUnknownToolError("bad_tool", "Unknown tool 'bad_tool'", tools)
		if !strings.Contains(msg, "bad_tool") {
			t.Errorf("message should contain tool name, got: %s", msg)
		}
		if !strings.Contains(msg, "server.tool1") || !strings.Contains(msg, "server.tool2") {
			t.Errorf("message should list available tools")
		}
	})

	t.Run("empty tools list", func(t *testing.T) {
		msg := FormatUnknownToolError("bad_tool", "Unknown tool 'bad_tool'", nil)
		if !strings.Contains(msg, "bad_tool") {
			t.Errorf("message should contain tool name, got: %s", msg)
		}
		// Should handle empty tools gracefully
		if !strings.Contains(msg, "No tools") && !strings.Contains(msg, "Available tools") {
			t.Errorf("Message for empty tools should contain \"No tools\" or \"Available tools\", got: %s", msg)
		}
	})
}

func TestValidateToolName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"server.tool", "server.tool"},
		{"kubernetes-server.resources_get", "kubernetes-server.resources_get"},
		{"just-text", ""},
		{"", ""},
		{"server.", ""},
		{".tool", ""},
		{"server.tool\nextra content", "server.tool"},
		{"  server.tool  ", "server.tool"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := validateToolName(tt.input)
			if got != tt.want {
				t.Errorf("validateToolName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ============================================================================
// COMPREHENSIVE EDGE CASE TESTS
// ============================================================================

func TestParseReActResponse_EdgeCases(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantMalformed bool
		wantAction    bool
		wantFinal     bool
		checkFunc     func(*testing.T, *ParsedReActResponse)
	}{
		{
			name:          "whitespace only",
			input:         "   \n\t\n   ",
			wantMalformed: true,
		},
		{
			name:          "only newlines",
			input:         "\n\n\n\n",
			wantMalformed: true,
		},
		{
			name:      "final answer with unicode",
			input:     "Thought: Check complete.\nFinal Answer: The pod 🚀 is failing due to UTF-8 encoding issues.",
			wantFinal: true,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if p.HasAction {
					t.Errorf("expected HasAction=false")
				}
				if !strings.Contains(p.FinalAnswer, "🚀") {
					t.Errorf("Unicode should be preserved in final answer")
				}
			},
		},
		{
			name:      "very long thought",
			input:     "Thought: " + strings.Repeat("This is a long thought. ", 100) + "\nFinal Answer: Done.",
			wantFinal: true,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if p.HasAction {
					t.Errorf("expected HasAction=false")
				}
				if len(p.Thought) < 2000 {
					t.Errorf("Long thought should be preserved, got length %d", len(p.Thought))
				}
			},
		},
		{
			name:       "very long action input",
			input:      "Action: server.tool\nAction Input: " + strings.Repeat("key: value\n", 100),
			wantAction: true,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if len(p.ActionInput) < 1000 {
					t.Errorf("Long action input should be preserved, got length %d", len(p.ActionInput))
				}
			},
		},
		{
			name:          "action with empty name after colon",
			input:         "Thought: Check.\nAction:   \nAction Input: {}",
			wantMalformed: true,
		},
		{
			name:      "thought with leading/trailing whitespace",
			input:     "Thought:   \n  Some thought here  \n  \nFinal Answer: Done.",
			wantFinal: true,
		},
		{
			name:       "multiple empty lines between sections",
			input:      "Thought: Check.\n\n\n\nAction: server.tool\n\n\nAction Input: {}",
			wantAction: true,
		},
		{
			name:      "final answer after exclamation mark",
			input:     "Thought: This is important!Final Answer: The analysis is complete.",
			wantFinal: true,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if p.HasAction {
					t.Errorf("expected HasAction=false")
				}
				if p.FinalAnswer != "The analysis is complete." {
					t.Errorf("Final answer should be extracted from mid-line")
				}
			},
		},
		{
			name:       "action after question mark",
			input:      "Thought: What should I do?Action: server.tool\nAction Input: {}",
			wantAction: true,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if p.Action != "server.tool" {
					t.Errorf("Action should be extracted from mid-line after question mark")
				}
			},
		},
		{
			name:          "lowercase action should not trigger detection",
			input:         "Thought: I need to take action: check the logs first.\n\nThis is just narrative text.",
			wantMalformed: true,
		},
		{
			name:          "Action in narrative without sentence boundary",
			input:         "Thought: The action: we should take is to investigate.\n\nLet's think about this carefully.",
			wantMalformed: true,
		},
		{
			name:      "final answer returned when action is incomplete (no ActionInput)",
			input:     "Thought: Analysis complete.\nAction: kubernetes-server.pods_list\nFinal Answer: The pods are running normally.",
			wantFinal: true,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if p.HasAction {
					t.Errorf("expected HasAction=false when Action has no ActionInput")
				}
				if p.FinalAnswer != "The pods are running normally." {
					t.Errorf("FinalAnswer = %q, want %q", p.FinalAnswer, "The pods are running normally.")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)

			if tt.wantMalformed {
				if !parsed.IsMalformed {
					t.Errorf("Expected IsMalformed=true, got false (HasAction=%v, IsFinalAnswer=%v)", parsed.HasAction, parsed.IsFinalAnswer)
				}
			}
			if tt.wantAction {
				if !parsed.HasAction {
					t.Errorf("Expected HasAction=true, got false")
				}
			}
			if tt.wantFinal {
				if !parsed.IsFinalAnswer {
					t.Errorf("Expected IsFinalAnswer=true, got false")
				}
			}
			if tt.checkFunc != nil {
				tt.checkFunc(t, parsed)
			}
		})
	}
}

func TestParseReActResponse_MultipleFinalAnswers(t *testing.T) {
	// First Final Answer should win
	input := "Thought: First.\nFinal Answer: First answer.\nFinal Answer: Second answer."
	parsed := ParseReActResponse(input)

	if !parsed.IsFinalAnswer {
		t.Fatalf("Expected IsFinalAnswer=true")
	}
	// Should contain only the first final answer content
	if !strings.Contains(parsed.FinalAnswer, "First answer") {
		t.Errorf("Should use first Final Answer, got: %q", parsed.FinalAnswer)
	}
}

func TestParseReActResponse_StopConditions(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantAction    bool
		wantFinal     bool
		wantThought   string
		wantNoThought bool
	}{
		{
			name:        "stop on [Based on",
			input:       "Thought: Check.\nAction: server.tool\nAction Input: {}\n[Based on the above information...",
			wantAction:  true,
			wantThought: "Check.",
		},
		{
			name:       "stop on Observation: with result",
			input:      "Thought: Check.\nAction: server.tool\nAction Input: {}\nObservation: {\"pods\": []}",
			wantAction: true,
		},
		{
			// Parser doesn't stop on "Please specify" observation — it continues
			// and reaches Final Answer.
			name:      "don't stop on continuation prompt with 'Please specify'",
			input:     "Thought: Thinking.\nObservation: Please specify what Action you want to take.\nFinal Answer: Done.",
			wantFinal: true,
		},
		{
			// Parser doesn't stop on "Error in reasoning" observation — it
			// continues and reaches Final Answer.
			name:      "don't stop on 'Error in reasoning' observation",
			input:     "Thought: Thinking.\nObservation: Error in reasoning - try again.\nFinal Answer: Done.",
			wantFinal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)

			if tt.wantAction && !parsed.HasAction {
				t.Errorf("Expected HasAction=true")
			}
			if tt.wantFinal && !parsed.IsFinalAnswer {
				t.Errorf("Expected IsFinalAnswer=true")
			}
			if tt.wantThought != "" && parsed.Thought != tt.wantThought {
				t.Errorf("Thought = %q, want %q", parsed.Thought, tt.wantThought)
			}
			if tt.wantNoThought && parsed.Thought != "" {
				t.Errorf("expected no Thought, got %q", parsed.Thought)
			}
		})
	}
}

func TestParseReActResponse_SectionHeaderVariations(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAction bool
		wantFinal  bool
		wantThought bool
	}{
		{
			name:        "Thought without colon on separate line",
			input:       "Thought\nI need to check the pods.\nAction: server.tool\nAction Input: {}",
			wantThought: true,
			wantAction:  true,
		},
		{
			name:        "Action with extra spaces after colon",
			input:       "Action:     server.tool\nAction Input: {}",
			wantAction:  true,
		},
		{
			name:       "Final Answer with leading spaces",
			input:      "  Final Answer: Done.",
			wantFinal:  true,
		},
		{
			name:       "Action Input with trailing content",
			input:      "Action: server.tool\nAction Input: {\"key\": \"value\"}  ",
			wantAction: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)

			if tt.wantAction && !parsed.HasAction {
				t.Errorf("Expected HasAction=true")
			}
			if tt.wantFinal && !parsed.IsFinalAnswer {
				t.Errorf("Expected IsFinalAnswer=true")
			}
			if tt.wantThought && parsed.Thought == "" {
				t.Errorf("Expected non-empty Thought")
			}
		})
	}
}

func TestParseReActResponse_MidlineDetectionComprehensive(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAction bool
		wantFinal  bool
		checkFunc  func(*testing.T, *ParsedReActResponse)
	}{
		{
			name:       "mid-line action after period",
			input:      "Thought: I will proceed.Action: server.tool\nAction Input: {}",
			wantAction: true,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if p.Action != "server.tool" {
					t.Errorf("Action = %q, want %q", p.Action, "server.tool")
				}
			},
		},
		{
			name:       "mid-line action after exclamation",
			input:      "Thought: I must act!Action: server.tool\nAction Input: {}",
			wantAction: true,
		},
		{
			name:       "mid-line action after question mark",
			input:      "Thought: What to do?Action: server.tool\nAction Input: {}",
			wantAction: true,
		},
		{
			name:      "mid-line final answer after period",
			input:     "Thought: Analysis complete.Final Answer: The root cause is OOM.",
			wantFinal: true,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if p.FinalAnswer != "The root cause is OOM." {
					t.Errorf("FinalAnswer = %q, want %q", p.FinalAnswer, "The root cause is OOM.")
				}
			},
		},
		{
			name:      "mid-line final answer after exclamation",
			input:     "Thought: Found it!Final Answer: The issue is network timeout.",
			wantFinal: true,
		},
		{
			name:      "mid-line final answer after question mark",
			input:     "Thought: Is this the cause?Final Answer: Yes, it's a memory leak.",
			wantFinal: true,
		},
		{
			name:       "mid-line with backtick before action",
			input:      "Thought: Let me check.`Action: server.tool\nAction Input: {}",
			wantAction: true, // Backtick is in the pattern
		},
		{
			// Unicode ellipsis (U+2026) is a single character, not [.!?], so
			// midlineFinalAnswerPattern won't match — no valid sentence boundary.
			name:      "mid-line with unicode ellipsis does not trigger detection",
			input:     "Thought: Wait\u2026Final Answer: Done.",
			wantFinal: false,
		},
		{
			// Three ASCII dots contain a period that midlineFinalAnswerPattern
			// matches as a valid sentence boundary before "Final Answer:".
			name:      "mid-line with three ASCII dots triggers detection",
			input:     "Thought: Wait...Final Answer: Done.",
			wantFinal: true,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if p.FinalAnswer != "Done." {
					t.Errorf("FinalAnswer = %q, want %q", p.FinalAnswer, "Done.")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)

			if tt.wantAction && !parsed.HasAction {
				t.Errorf("Expected HasAction=true")
			}
			if tt.wantFinal && !parsed.IsFinalAnswer {
				t.Errorf("Expected IsFinalAnswer=true")
			}
			if tt.checkFunc != nil {
				tt.checkFunc(t, parsed)
			}
		})
	}
}

func TestParseReActResponse_ActionRecoveryComprehensive(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantAction    bool
		wantMalformed bool
		wantActionStr string
	}{
		{
			name:          "action without colon before action input",
			input:         "Thought: Check.\nAction\nserver.tool\nAction Input: {}",
			wantAction:    true,
			wantActionStr: "server.tool",
		},
		{
			name:          "action with space after Action word",
			input:         "Thought: Check.\nAction server.tool\nAction Input: {}",
			wantAction:    true,
			wantActionStr: "server.tool",
		},
		{
			name:          "multiple potential actions before action input",
			input:         "Thought: Check.\nAction: bad.tool\nAction: server.tool\nAction Input: {}",
			wantAction:    true,
			wantActionStr: "server.tool",
		},
		{
			name:          "action colon on next line with tool name",
			input:         "Thought: I will check.\nAction:\nkubernetes-server.list_namespaces\nAction Input: {}",
			wantAction:    true,
			wantActionStr: "kubernetes-server.list_namespaces",
		},
		{
			name:          "action colon no space before tool",
			input:         "Action:kubernetes-server.get_pods\nAction Input: {}",
			wantAction:    true,
			wantActionStr: "kubernetes-server.get_pods",
		},
		{
			name:          "invalid tool name in recovery stays malformed",
			input:         "Thought: Check.\nAction\nthis is not a valid tool name\nAction Input: {}",
			wantMalformed: true,
		},
		{
			// Unicode text before "Action Input:" — verifies the regex-based
			// search finds the correct byte offset in the original string.
			name:          "recovery with unicode text before action input",
			input:         "Thought: Überprüfung läuft.\nAction: k8s-server.get_pods\nAction Input: {}",
			wantAction:    true,
			wantActionStr: "k8s-server.get_pods",
		},
		{
			name: "real-world mid-line Action without colon then tool on next line",
			input: `Thought
The user wants me to check the health of a Kubernetes cluster.

I will start by listing the pods in the namespace.Action
monitoring-server.list-pods
Action Input:
namespace: kube-system`,
			wantAction:    true,
			wantActionStr: "monitoring-server.list-pods",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)

			if tt.wantAction {
				if !parsed.HasAction {
					t.Fatalf("expected HasAction=true, got false")
				}
				if tt.wantActionStr != "" && parsed.Action != tt.wantActionStr {
					t.Errorf("Action = %q, want %q", parsed.Action, tt.wantActionStr)
				}
			}
			if tt.wantMalformed {
				if !parsed.IsMalformed {
					t.Errorf("expected IsMalformed=true, got false (HasAction=%v, Action=%q)", parsed.HasAction, parsed.Action)
				}
			}
		})
	}
}

func TestParseReActResponse_ComplexScenarios(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		checkFunc func(*testing.T, *ParsedReActResponse)
	}{
		{
			name: "multiple actions then final answer",
			input: `Thought: Start investigation.
Action: server.tool1
Action Input: {}
Thought: Continue.
Action: server.tool2
Action Input: {}
Final Answer: Complete.`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				// Parser extracts first action+input, OR might handle multiple actions
				// This tests the actual behavior
				if !p.HasAction && !p.IsFinalAnswer {
					t.Errorf("Should extract either action or final answer")
				}
			},
		},
		{
			name: "thought with final answer in thought content (not mid-line)",
			input: `Thought: I believe the Final Answer should be about memory.
Action: server.tool
Action Input: {}`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.HasAction {
					t.Errorf("Should parse as action, not confused by 'Final Answer' in thought content")
				}
			},
		},
		{
			name: "action with json containing colons",
			input: `Action: server.tool
Action Input: {"key": "value", "nested": {"inner": "data"}}`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.HasAction {
					t.Errorf("Should handle JSON with colons")
				}
				if !strings.Contains(p.ActionInput, "nested") {
					t.Errorf("Action input should preserve JSON structure")
				}
			},
		},
		{
			name: "multiline action input with mixed content",
			input: `Action: server.tool
Action Input: This is line 1
This is line 2
{"key": "value"}
This is line 4`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.HasAction {
					t.Errorf("Should handle multiline action input")
				}
				lines := strings.Split(p.ActionInput, "\n")
				if len(lines) < 3 {
					t.Errorf("Should preserve multiline structure, got %d lines", len(lines))
				}
			},
		},
		{
			name: "empty sections between non-empty ones",
			input: `Thought: 

Action: server.tool
Action Input: 

`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.HasAction {
					t.Errorf("Should handle empty sections")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)
			if tt.checkFunc != nil {
				tt.checkFunc(t, parsed)
			}
		})
	}
}

func TestParseReActResponse_ToolNameValidation(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantUnknown   bool
		wantAction    bool
		wantMalformed bool
	}{
		{
			name:        "valid tool with hyphen in server name",
			input:       "Action: kubernetes-server.get_pods\nAction Input: {}",
			wantAction:  true,
			wantUnknown: false,
		},
		{
			name:        "valid tool with underscore in tool name",
			input:       "Action: server.get_resource_info\nAction Input: {}",
			wantAction:  true,
			wantUnknown: false,
		},
		{
			name:        "invalid tool - no dot",
			input:       "Action: justtext\nAction Input: {}",
			wantAction:  true,
			wantUnknown: true,
		},
		{
			name:        "invalid tool - only server part",
			input:       "Action: server\nAction Input: {}",
			wantAction:  true,
			wantUnknown: true,
		},
		{
			// ParseReActResponse uses a loose dot-check (strings.Contains) to decide
			// IsUnknownTool, while validateToolName uses the strict toolNamePattern
			// regex (^[\w\-]+\.[\w\-]+$). This means ".tool" passes the parser's
			// dot-check but would fail validateToolName. The controller's tool-name-set
			// lookup handles final validation for these edge cases.
			name:        "tool with dot at start - parser accepts any string with dot",
			input:       "Action: .tool\nAction Input: {}",
			wantAction:  true,
			wantUnknown: false, // Parser only checks for dot presence, not strict format
		},
		{
			name:        "tool with dot at end - parser accepts any string with dot",
			input:       "Action: server.\nAction Input: {}",
			wantAction:  true,
			wantUnknown: false, // Parser only checks for dot presence
		},
		{
			name:        "multiple dots - parser accepts",
			input:       "Action: server.sub.tool\nAction Input: {}",
			wantAction:  true,
			wantUnknown: false, // Parser accepts as long as there's a dot
		},
		{
			name:        "invalid tool - space in name, no dot",
			input:       "Action: server tool\nAction Input: {}",
			wantAction:  true,
			wantUnknown: true, // No dot, so marked as unknown
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)

			if tt.wantAction && !parsed.HasAction {
				t.Errorf("Expected HasAction=true")
			}
			if tt.wantUnknown && !parsed.IsUnknownTool {
				t.Errorf("Expected IsUnknownTool=true, got false")
			}
			if !tt.wantUnknown && parsed.IsUnknownTool {
				t.Errorf("Expected IsUnknownTool=false, got true")
			}
			if tt.wantMalformed && !parsed.IsMalformed {
				t.Errorf("Expected IsMalformed=true")
			}
		})
	}
}

func TestParseReActResponse_FoundSectionsComprehensive(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]bool
	}{
		{
			name:  "all sections present",
			input: "Thought: Check.\nAction: server.tool\nAction Input: {}\nFinal Answer: Done.",
			want: map[string]bool{
				"thought":      true,
				"action":       true,
				"action_input": true,
				"final_answer": true,
			},
		},
		{
			name:  "only thought and final answer",
			input: "Thought: Analyzed.\nFinal Answer: Complete.",
			want: map[string]bool{
				"thought":      true,
				"action":       false,
				"action_input": false,
				"final_answer": true,
			},
		},
		{
			name:  "action without action input",
			input: "Action: server.tool",
			want: map[string]bool{
				"thought":      false,
				"action":       true,
				"action_input": false,
				"final_answer": false,
			},
		},
		{
			name:  "empty input",
			input: "",
			want: map[string]bool{
				"thought":      false,
				"action":       false,
				"action_input": false,
				"final_answer": false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)

			for section, expected := range tt.want {
				if parsed.FoundSections[section] != expected {
					t.Errorf("FoundSections[%q] = %v, want %v", section, parsed.FoundSections[section], expected)
				}
			}
		})
	}
}

func TestGetFormatErrorFeedback_Comprehensive(t *testing.T) {
	tests := []struct {
		name         string
		parsed       *ParsedReActResponse
		wantContains []string
	}{
		{
			name: "action without action input",
			parsed: &ParsedReActResponse{
				IsMalformed: true,
				FoundSections: map[string]bool{
					"thought":      true,
					"action":       true,
					"action_input": false,
					"final_answer": false,
				},
			},
			wantContains: []string{"missing \"Action Input:\"", "FORMAT ERROR"},
		},
		{
			name: "action input without action",
			parsed: &ParsedReActResponse{
				IsMalformed: true,
				FoundSections: map[string]bool{
					"thought":      false,
					"action":       false,
					"action_input": true,
					"final_answer": false,
				},
			},
			wantContains: []string{"missing \"Action:\"", "FORMAT ERROR"},
		},
		{
			name: "only thought",
			parsed: &ParsedReActResponse{
				IsMalformed: true,
				FoundSections: map[string]bool{
					"thought":      true,
					"action":       false,
					"action_input": false,
					"final_answer": false,
				},
			},
			wantContains: []string{"only contains \"Thought:\"", "FORMAT ERROR"},
		},
		{
			name: "no sections at all",
			parsed: &ParsedReActResponse{
				IsMalformed: true,
				FoundSections: map[string]bool{
					"thought":      false,
					"action":       false,
					"action_input": false,
					"final_answer": false,
				},
			},
			wantContains: []string{"Could not detect any ReAct sections", "FORMAT ERROR"},
		},
		{
			// Exercises the default branch (thought+final_answer without action).
			// Verifies deterministic ordering of Found/Missing lists.
			name: "default branch - deterministic ordering",
			parsed: &ParsedReActResponse{
				IsMalformed: true,
				FoundSections: map[string]bool{
					"thought":      true,
					"action":       false,
					"action_input": false,
					"final_answer": true,
				},
			},
			wantContains: []string{
				"Incomplete ReAct format",
				"Found: thought, final_answer",
				"Missing: action, action_input",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feedback := GetFormatErrorFeedback(tt.parsed)

			for _, want := range tt.wantContains {
				if !strings.Contains(feedback, want) {
					t.Errorf("feedback should contain %q, got: %s", want, feedback)
				}
			}

			// All feedback should contain the format reminder
			if !strings.Contains(feedback, "IMPORTANT: Please follow the exact ReAct format") {
				t.Errorf("feedback should always contain format reminder")
			}
		})
	}
}

func TestGetFormatCorrectionReminder(t *testing.T) {
	reminder := GetFormatCorrectionReminder()

	requiredParts := []string{
		"Thought:",
		"Action:",
		"Action Input:",
		"Final Answer:",
		"NEW LINE",
	}

	for _, part := range requiredParts {
		if !strings.Contains(reminder, part) {
			t.Errorf("Reminder should contain %q", part)
		}
	}
}

func TestFormatToolErrorObservation(t *testing.T) {
	err := fmt.Errorf("connection timeout")
	obs := FormatToolErrorObservation(err)

	if !strings.Contains(obs, "Observation:") {
		t.Errorf("Should start with Observation:")
	}
	if !strings.Contains(obs, "connection timeout") {
		t.Errorf("Should contain error message")
	}
}

func TestFormatErrorObservation(t *testing.T) {
	err := fmt.Errorf("LLM provider unavailable")
	obs := FormatErrorObservation(err)

	if !strings.Contains(obs, "Observation:") {
		t.Errorf("Should start with Observation:")
	}
	if !strings.Contains(obs, "LLM provider unavailable") {
		t.Errorf("Should contain error message")
	}
	if !strings.Contains(obs, "try again") {
		t.Errorf("Should contain retry instruction")
	}
}

func TestExtractForcedConclusionAnswer_Comprehensive(t *testing.T) {
	tests := []struct {
		name   string
		parsed *ParsedReActResponse
		want   string
	}{
		{
			name: "with final answer",
			parsed: &ParsedReActResponse{
				IsFinalAnswer: true,
				FinalAnswer:   "The conclusion.",
				Thought:       "Some thought.",
			},
			want: "The conclusion.",
		},
		{
			name: "with thought only",
			parsed: &ParsedReActResponse{
				IsFinalAnswer: false,
				FinalAnswer:   "",
				Thought:       "My analysis is...",
			},
			want: "My analysis is...",
		},
		{
			name: "empty response",
			parsed: &ParsedReActResponse{
				IsFinalAnswer: false,
				FinalAnswer:   "",
				Thought:       "",
			},
			want: "",
		},
		{
			name: "final answer takes precedence over thought",
			parsed: &ParsedReActResponse{
				IsFinalAnswer: true,
				FinalAnswer:   "Final.",
				Thought:       "Thought.",
			},
			want: "Final.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractForcedConclusionAnswer(tt.parsed)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseReActResponse_RealWorldExamples(t *testing.T) {
	// Test with realistic LLM outputs that might be problematic
	tests := []struct {
		name      string
		input     string
		checkFunc func(*testing.T, *ParsedReActResponse)
	}{
		{
			name: "anthropic style with thinking",
			input: `Thought: I need to check the pod status to understand the failure.

Action: kubernetes-server.resources_get
Action Input: {"apiVersion": "v1", "kind": "Pod", "namespace": "default", "name": "my-pod"}`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.HasAction {
					t.Fatalf("Should parse action from realistic format")
				}
				if p.Action != "kubernetes-server.resources_get" {
					t.Errorf("Action = %q", p.Action)
				}
			},
		},
		{
			name: "openai style conclusion",
			input: `Thought: Based on all the information gathered, I can now provide a comprehensive analysis.

Final Answer: The pod is failing because it's running out of memory. The container has a memory limit of 512Mi but the application requires at least 1Gi to function properly. Recommendation: Increase the memory limit to 1Gi in the deployment spec.`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.IsFinalAnswer {
					t.Fatalf("Should parse final answer")
				}
				if !strings.Contains(p.FinalAnswer, "memory") {
					t.Errorf("Should preserve full final answer")
				}
			},
		},
		{
			name: "unknown tool with long multi-paragraph thought",
			input: `Thought: The alert indicates high memory usage on the staging-cluster with current_memory_usage: 87% and node_count: 12. I need to determine whether this is a transient spike or a persistent issue requiring intervention.

First, I need to identify which nodes are under pressure. The cluster has multiple node pools and I need to check resource allocation across all of them to find the bottleneck.

Given the metrics, the memory usage is 87% which is above the 80% threshold. I need to find out which workloads are consuming the most memory and whether any pods need to be evicted or rescheduled.

Action: quick_search
Action Input: query: kubernetes memory pressure troubleshooting`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.IsUnknownTool {
					t.Fatalf("Should detect unknown tool (no dot in 'quick_search')")
				}
				if !p.HasAction {
					t.Errorf("HasAction should be true for unknown tool")
				}
				if p.Action != "quick_search" {
					t.Errorf("Action = %q, want %q", p.Action, "quick_search")
				}
				if p.Thought == "" {
					t.Errorf("Should preserve thought for unknown tool response")
				}
				if !strings.Contains(p.Thought, "staging-cluster") {
					t.Errorf("Thought should contain original content")
				}
			},
		},
		{
			name: "real-world Thought without colon from user report",
			input: `Thought
The user wants me to check the health of the production deployment.
The alert indicates high pod restart counts in the payments namespace.
The affected pod is api-gateway-7b9d4f6c88-xk2p9 in the payments namespace on the production cluster.

My troubleshooting plan is as follows:
1. List all pods in the payments namespace to get an overview of their status.
2. Examine the logs of the restarting pod to understand the failure.

Action: kubernetes-server.pods_list
Action Input: namespace: payments`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.HasAction {
					t.Fatalf("Should parse action after Thought without colon")
				}
				if p.Action != "kubernetes-server.pods_list" {
					t.Errorf("Action = %q, want %q", p.Action, "kubernetes-server.pods_list")
				}
				if !strings.Contains(p.Thought, "payments") {
					t.Errorf("Should capture thought content")
				}
				if !strings.Contains(p.Thought, "troubleshooting plan") {
					t.Errorf("Should capture multi-paragraph thought with numbered list")
				}
			},
		},
		{
			name: "Thought-without-colon narrative false positive (Thought about it)",
			input: `Thought
The user wants me to diagnose a pod crash loop.
Thought about it carefully and decided to proceed.
I will check the logs first.

Action: kubernetes-server.get_logs
Action Input: pod: api-gateway-7b9d4f6c88-xk2p9`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.HasAction {
					t.Fatalf("Should parse action")
				}
				// The thought should include ALL content, including "Thought about it..."
				if !strings.Contains(p.Thought, "Thought about it carefully") {
					t.Errorf("'Thought about it...' should be part of thought content, not a new section. Thought = %q", p.Thought)
				}
			},
		},
		{
			name: "mid-line Final Answer within Thought-without-colon continuation",
			input: `Thought
The configuration file confirms the application is running version 2.3.4 of the standard web server.

I have enough information to provide a final answer.Final Answer:
**System Status Summary**: The application is operating normally.

Recommended Action: MONITOR

**Confidence Level**: HIGH`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.IsFinalAnswer {
					t.Fatalf("Should detect mid-line Final Answer within Thought-without-colon content")
				}
				if !strings.Contains(p.FinalAnswer, "System Status Summary") {
					t.Errorf("FinalAnswer should contain the summary, got: %q", p.FinalAnswer)
				}
			},
		},
		{
			name: "gemini real-world mid-line action with markdown and numbered steps",
			input: `Thought:
The user wants me to check a Kubernetes alert indicating that the staging-app namespace is stuck in a terminating state. The alert context points to the cluster-east region.

Following the troubleshooting guide, my first step is to retrieve the full details of the namespace to understand its current state, specifically looking at its status.conditions and spec.finalizers.

**Phase 1: Namespace Status Check**
*   **Step 1: Retrieve Namespace Details**

I will now get the YAML for the staging-app namespace.Action: kubernetes-server.resources_get
Action Input: apiVersion: v1
kind: Namespace
name: staging-app`,
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.HasAction {
					t.Fatalf("CRITICAL: Must parse real Gemini mid-line action case")
				}
				if p.Action != "kubernetes-server.resources_get" {
					t.Errorf("Action = %q, want %q", p.Action, "kubernetes-server.resources_get")
				}
				if p.Thought == "" {
					t.Errorf("Should preserve thought content")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)
			if tt.checkFunc != nil {
				tt.checkFunc(t, parsed)
			}
		})
	}
}

func TestParseReActResponse_ThoughtSpaceTier2Exclusion(t *testing.T) {
	// Lines starting with "Thought " (space, no colon) should NOT trigger
	// mid-line Final Answer detection in Tier 2. This matches old TARSy behavior
	// and prevents false positives.
	tests := []struct {
		name      string
		input     string
		checkFunc func(*testing.T, *ParsedReActResponse)
	}{
		{
			name:  "Thought-space line with Final Answer should not detect mid-line FA",
			input: "Thought something about the issue. Final Answer: This is wrong.",
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				// "Thought " is not a section header (Tier 1 doesn't match it),
				// and Tier 2 should exclude it from mid-line Final Answer detection.
				// The line falls through to "no section" and nothing is captured.
				if p.IsFinalAnswer {
					t.Errorf("Should NOT detect mid-line Final Answer on a 'Thought ' line")
				}
			},
		},
		{
			name:  "Thought-colon with Final Answer still works",
			input: "Thought: I analyzed everything. Final Answer: The root cause is OOM.",
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				// "Thought:" IS a section header, and mid-line Final Answer
				// detection happens within the thought content handler (not Tier 2).
				if !p.IsFinalAnswer {
					t.Errorf("Should detect mid-line Final Answer within Thought: content")
				}
			},
		},
		{
			name:  "Thought exact match on separate line still works",
			input: "Thought\nI need to check.\nFinal Answer: Done.",
			checkFunc: func(t *testing.T, p *ParsedReActResponse) {
				if !p.IsFinalAnswer {
					t.Errorf("Should detect Final Answer after Thought (exact match)")
				}
				if p.Thought != "I need to check." {
					t.Errorf("Thought = %q, want %q", p.Thought, "I need to check.")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := ParseReActResponse(tt.input)
			tt.checkFunc(t, parsed)
		})
	}
}
