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
		if agent == nil || agent.Description == "" || agent.Type == AgentTypeOrchestrator {
			continue
		}
		entry := SubAgentEntry{
			Name:        name,
			Description: agent.Description,
		}
		if len(agent.MCPServers) > 0 {
			entry.MCPServers = make([]string, len(agent.MCPServers))
			copy(entry.MCPServers, agent.MCPServers)
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

// Entries returns a deep copy of all entries in the registry.
func (r *SubAgentRegistry) Entries() []SubAgentEntry {
	out := make([]SubAgentEntry, len(r.entries))
	for i, e := range r.entries {
		out[i] = e.clone()
	}
	return out
}

func (e SubAgentEntry) clone() SubAgentEntry {
	c := e
	if len(e.MCPServers) > 0 {
		c.MCPServers = make([]string, len(e.MCPServers))
		copy(c.MCPServers, e.MCPServers)
	}
	if len(e.NativeTools) > 0 {
		c.NativeTools = make([]string, len(e.NativeTools))
		copy(c.NativeTools, e.NativeTools)
	}
	return c
}

// Get returns the entry for the given agent name, or false if not found.
func (r *SubAgentRegistry) Get(name string) (SubAgentEntry, bool) {
	for _, e := range r.entries {
		if e.Name == name {
			return e.clone(), true
		}
	}
	return SubAgentEntry{}, false
}

// Filter returns a new registry containing only agents whose names are in allowedNames.
// If allowedNames is nil, returns a new registry with a copy of all entries.
func (r *SubAgentRegistry) Filter(allowedNames []string) *SubAgentRegistry {
	if allowedNames == nil {
		copied := make([]SubAgentEntry, len(r.entries))
		for i, e := range r.entries {
			copied[i] = e.clone()
		}
		return &SubAgentRegistry{entries: copied}
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
