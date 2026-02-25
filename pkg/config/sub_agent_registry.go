package config

import "sort"

// SubAgentEntry describes an agent available for orchestrator dispatch.
type SubAgentEntry struct {
	Name        string
	Description string
	MCPServers  []string
	NativeTools []string
}

// SubAgentRegistry holds agents eligible for orchestrator dispatch.
type SubAgentRegistry struct {
	entries []SubAgentEntry
}

// BuildSubAgentRegistry creates a registry from the merged agent map.
// Includes agents with non-empty Description, excludes orchestrator agents.
func BuildSubAgentRegistry(agents map[string]*AgentConfig) *SubAgentRegistry {
	var entries []SubAgentEntry
	for name, agent := range agents {
		if agent.Description == "" || agent.Type == AgentTypeOrchestrator {
			continue
		}
		entry := SubAgentEntry{
			Name:        name,
			Description: agent.Description,
			MCPServers:  agent.MCPServers,
		}
		for tool, enabled := range agent.NativeTools {
			if enabled {
				entry.NativeTools = append(entry.NativeTools, string(tool))
			}
		}
		sort.Strings(entry.NativeTools)
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return &SubAgentRegistry{entries: entries}
}

// Entries returns all entries in the registry.
func (r *SubAgentRegistry) Entries() []SubAgentEntry {
	return r.entries
}

// Filter returns a new registry containing only agents whose names are in allowedNames.
// If allowedNames is nil, returns the full registry (no copy — caller must not mutate).
func (r *SubAgentRegistry) Filter(allowedNames []string) *SubAgentRegistry {
	if allowedNames == nil {
		return r
	}
	allowed := make(map[string]bool, len(allowedNames))
	for _, name := range allowedNames {
		allowed[name] = true
	}
	var filtered []SubAgentEntry
	for _, entry := range r.entries {
		if allowed[entry.Name] {
			filtered = append(filtered, entry)
		}
	}
	return &SubAgentRegistry{entries: filtered}
}
