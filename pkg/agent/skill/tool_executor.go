package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// Compile-time check that SkillToolExecutor implements agent.ToolExecutor.
var _ agent.ToolExecutor = (*SkillToolExecutor)(nil)

const toolLoadSkill = "load_skill"

// loadSkillTool is the tool definition exposed to the LLM.
var loadSkillTool = agent.ToolDefinition{
	Name:        toolLoadSkill,
	Description: "Load domain knowledge skills by name. Returns the full skill content.",
	ParametersSchema: `{
  "type": "object",
  "properties": {
    "names": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Skill names to load"
    }
  },
  "required": ["names"]
}`,
}

// SkillToolExecutor wraps an inner ToolExecutor and intercepts load_skill
// calls. Everything else passes through to the inner executor.
// Same wrapping pattern as orchestrator.CompositeToolExecutor.
type SkillToolExecutor struct {
	inner        agent.ToolExecutor
	registry     *config.SkillRegistry
	allowedNames map[string]struct{}
}

// NewSkillToolExecutor creates a skill executor. inner may be nil and is
// safely handled by ListTools, Execute, and Close.
// allowedNames restricts which skills from the registry are available.
func NewSkillToolExecutor(
	inner agent.ToolExecutor,
	registry *config.SkillRegistry,
	allowedNames map[string]struct{},
) *SkillToolExecutor {
	return &SkillToolExecutor{
		inner:        inner,
		registry:     registry,
		allowedNames: allowedNames,
	}
}

// ListTools returns the combined tool set: load_skill + inner tools.
func (s *SkillToolExecutor) ListTools(ctx context.Context) ([]agent.ToolDefinition, error) {
	tools := []agent.ToolDefinition{loadSkillTool}

	if s.inner != nil {
		innerTools, err := s.inner.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list inner tools: %w", err)
		}
		tools = append(tools, innerTools...)
	}

	return tools, nil
}

// Execute routes the tool call to load_skill or the inner executor.
func (s *SkillToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
	if call.Name == toolLoadSkill {
		return s.executeLoadSkill(call)
	}
	if s.inner != nil {
		return s.inner.Execute(ctx, call)
	}
	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: fmt.Sprintf("unknown tool: %s", call.Name),
		IsError: true,
	}, nil
}

// Close delegates to the inner executor.
func (s *SkillToolExecutor) Close() error {
	if s.inner != nil {
		return s.inner.Close()
	}
	return nil
}

func (s *SkillToolExecutor) executeLoadSkill(call agent.ToolCall) (*agent.ToolResult, error) {
	var args struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("invalid arguments: %v", err),
			IsError: true,
		}, nil
	}
	if len(args.Names) == 0 {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: "'names' must contain at least one skill name",
			IsError: true,
		}, nil
	}

	var loaded []string
	var invalidNames []string

	for _, name := range args.Names {
		if _, allowed := s.allowedNames[name]; !allowed {
			invalidNames = append(invalidNames, name)
			continue
		}
		if s.registry == nil {
			invalidNames = append(invalidNames, name)
			continue
		}
		skill, err := s.registry.Get(name)
		if err != nil {
			invalidNames = append(invalidNames, name)
			continue
		}
		loaded = append(loaded, fmt.Sprintf("## Skill: %s\n\n%s", skill.Name, skill.Body))
	}

	// All names invalid → error
	if len(loaded) == 0 {
		return &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("no valid skills found for: %s\n\nAvailable skills: %s", strings.Join(invalidNames, ", "), s.availableSkillList()),
			IsError: true,
		}, nil
	}

	content := strings.Join(loaded, "\n\n")

	// Partial failure: some valid, some invalid → append note
	if len(invalidNames) > 0 {
		content += fmt.Sprintf("\n\n---\nNote: the following skill names were not found: %s\n\nAvailable skills: %s", strings.Join(invalidNames, ", "), s.availableSkillList())
	}

	return &agent.ToolResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: content,
	}, nil
}

func (s *SkillToolExecutor) availableSkillList() string {
	names := make([]string, 0, len(s.allowedNames))
	for name := range s.allowedNames {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
