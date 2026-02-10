package masking

// Masker is the interface for code-based maskers that need structural awareness
// beyond regex pattern matching. Code-based maskers can parse YAML/JSON and
// apply context-sensitive masking (e.g., mask K8s Secrets but not ConfigMaps).
type Masker interface {
	// Name returns the unique identifier for this masker.
	// Must match the key in config.GetBuiltinConfig().CodeMaskers.
	Name() string

	// AppliesTo performs a lightweight check on whether this masker
	// should process the data. Should be fast (string contains, not parsing).
	AppliesTo(data string) bool

	// Mask applies masking logic and returns the masked result.
	// Must be defensive: return original data on parse/processing errors.
	Mask(data string) string
}
