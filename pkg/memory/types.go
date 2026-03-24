package memory

// Memory represents a stored investigation memory.
type Memory struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Category   string  `json:"category"`
	Valence    string  `json:"valence"`
	Confidence float64 `json:"confidence"`
	SeenCount  int     `json:"seen_count"`
}

// ReflectorResult holds the parsed output from a Reflector LLM call.
type ReflectorResult struct {
	Create    []ReflectorCreateAction    `json:"create"`
	Reinforce []ReflectorReinforceAction `json:"reinforce"`
	Deprecate []ReflectorDeprecateAction `json:"deprecate"`
}

// IsEmpty returns true when the Reflector produced no actions.
func (r *ReflectorResult) IsEmpty() bool {
	return len(r.Create) == 0 && len(r.Reinforce) == 0 && len(r.Deprecate) == 0
}

// ReflectorCreateAction describes a new memory to store.
type ReflectorCreateAction struct {
	Content  string `json:"content"`
	Category string `json:"category"`
	Valence  string `json:"valence"`
}

// ReflectorReinforceAction identifies an existing memory to reinforce.
type ReflectorReinforceAction struct {
	MemoryID string `json:"memory_id"`
}

// ReflectorDeprecateAction identifies an existing memory to deprecate.
type ReflectorDeprecateAction struct {
	MemoryID string `json:"memory_id"`
	Reason   string `json:"reason"`
}
