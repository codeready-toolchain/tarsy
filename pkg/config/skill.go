package config

import (
	"fmt"
	"slices"
	"sync"

	"maps"
)

// SkillConfig holds a parsed SKILL.md file.
type SkillConfig struct {
	Name        string // From frontmatter
	Description string // From frontmatter (catalog entry)
	Body        string // Markdown body (loaded on demand via load_skill tool)
}

// SkillRegistry stores skills in memory with thread-safe access.
// Same pattern as AgentRegistry, MCPServerRegistry.
type SkillRegistry struct {
	skills map[string]*SkillConfig
	mu     sync.RWMutex
}

// NewSkillRegistry creates a new skill registry with a defensive copy.
func NewSkillRegistry(skills map[string]*SkillConfig) *SkillRegistry {
	copied := make(map[string]*SkillConfig, len(skills))
	for k, v := range skills {
		copied[k] = v
	}
	return &SkillRegistry{
		skills: copied,
	}
}

// Get retrieves a skill configuration by name (thread-safe).
func (r *SkillRegistry) Get(name string) (*SkillConfig, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	skill, exists := r.skills[name]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrSkillNotFound, name)
	}
	return skill, nil
}

// GetAll returns all skill configurations (thread-safe, returns copy).
func (r *SkillRegistry) GetAll() map[string]*SkillConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*SkillConfig, len(r.skills))
	for k, v := range r.skills {
		result[k] = v
	}
	return result
}

// Has checks if a skill exists in the registry (thread-safe).
func (r *SkillRegistry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.skills[name]
	return exists
}

// Len returns the number of skills in the registry (thread-safe).
func (r *SkillRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.skills)
}

// Names returns a sorted list of all skill names (thread-safe).
// Sorted for deterministic catalog ordering in prompts.
func (r *SkillRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := slices.Collect(maps.Keys(r.skills))
	slices.Sort(names)
	return names
}
