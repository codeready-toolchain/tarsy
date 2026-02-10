package masking

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaskedSecretValue is the replacement string for masked Kubernetes Secret data values.
const MaskedSecretValue = "[MASKED_SECRET_DATA]"

// Pre-compiled patterns for fast AppliesTo checks.
var (
	yamlSecretPattern = regexp.MustCompile(`(?m)^kind:\s*Secret\s*$`)
	jsonSecretPattern = regexp.MustCompile(`"kind"\s*:\s*"Secret"`)
)

// KubernetesSecretMasker masks data/stringData fields in Kubernetes Secret resources
// while leaving ConfigMaps and other resource kinds untouched.
type KubernetesSecretMasker struct{}

// Name returns the unique identifier for this masker.
func (m *KubernetesSecretMasker) Name() string { return "kubernetes_secret" }

// AppliesTo performs a lightweight check on whether this masker should process the data.
func (m *KubernetesSecretMasker) AppliesTo(data string) bool {
	if !strings.Contains(data, "Secret") {
		return false
	}
	return yamlSecretPattern.MatchString(data) || jsonSecretPattern.MatchString(data)
}

// Mask applies Kubernetes Secret masking logic.
// Detects JSON vs YAML and applies the appropriate parser.
// Returns original data on parse/processing errors (defensive).
func (m *KubernetesSecretMasker) Mask(data string) string {
	trimmed := strings.TrimSpace(data)

	// Try JSON first when input looks like JSON (starts with { or [).
	// This prevents the YAML parser from consuming JSON and re-serializing as YAML.
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		if masked := m.maskJSON(data); masked != data {
			return masked
		}
	}

	// Try YAML (handles multi-document with --- separators)
	if masked := m.maskYAML(data); masked != data {
		return masked
	}

	return data
}

// maskYAML parses multi-document YAML and masks Secret resources.
func (m *KubernetesSecretMasker) maskYAML(data string) string {
	decoder := yaml.NewDecoder(strings.NewReader(data))
	var documents []map[string]any
	anySecret := false

	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return data // Parse error — return original (defensive)
		}
		if doc == nil {
			continue
		}

		if isKubernetesSecret(doc) {
			maskSecretFields(doc)
			maskAnnotationSecrets(doc)
			anySecret = true
		} else if isKubernetesList(doc) {
			if m.maskListItems(doc) {
				anySecret = true
			}
		}

		documents = append(documents, doc)
	}

	if !anySecret || len(documents) == 0 {
		return data // Nothing to mask
	}

	// Re-serialize to YAML preserving multi-document boundaries
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	for _, doc := range documents {
		if err := encoder.Encode(doc); err != nil {
			return data // Serialization error — return original (defensive)
		}
	}
	if err := encoder.Close(); err != nil {
		return data
	}

	result := buf.String()
	// yaml.Encoder always adds a trailing newline; trim to match original
	result = strings.TrimRight(result, "\n")
	if strings.HasSuffix(data, "\n") {
		result += "\n"
	}

	return result
}

// maskJSON parses JSON and masks Secret resources.
func (m *KubernetesSecretMasker) maskJSON(data string) string {
	// Try to parse as a JSON object
	var obj map[string]any
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return data // Not valid JSON — return original
	}

	anyMasked := false

	if isKubernetesSecret(obj) {
		maskSecretFields(obj)
		maskAnnotationSecrets(obj)
		anyMasked = true
	} else if isKubernetesList(obj) {
		if m.maskJSONListItems(obj) {
			anyMasked = true
		}
	}

	if !anyMasked {
		return data
	}

	// Re-serialize with indentation matching typical kubectl JSON output
	result, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return data
	}

	// Preserve trailing newline if original had one
	output := string(result)
	if strings.HasSuffix(data, "\n") {
		output += "\n"
	}

	return output
}

// maskListItems masks Secret items within a Kubernetes List in YAML-parsed data.
// Returns true if any items were masked.
func (m *KubernetesSecretMasker) maskListItems(doc map[string]any) bool {
	items, ok := doc["items"]
	if !ok {
		return false
	}

	itemList, ok := items.([]any)
	if !ok {
		return false
	}

	anyMasked := false
	for _, item := range itemList {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if isKubernetesSecret(itemMap) {
			maskSecretFields(itemMap)
			maskAnnotationSecrets(itemMap)
			anyMasked = true
		}
	}

	return anyMasked
}

// maskJSONListItems masks Secret items within a JSON List object.
// Returns true if any items were masked.
func (m *KubernetesSecretMasker) maskJSONListItems(obj map[string]any) bool {
	items, ok := obj["items"]
	if !ok {
		return false
	}

	itemList, ok := items.([]any)
	if !ok {
		return false
	}

	anyMasked := false
	for _, item := range itemList {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if isKubernetesSecret(itemMap) {
			maskSecretFields(itemMap)
			maskAnnotationSecrets(itemMap)
			anyMasked = true
		}
	}

	return anyMasked
}

// isKubernetesSecret checks if a resource map represents a Kubernetes Secret.
func isKubernetesSecret(resource map[string]any) bool {
	kind, ok := resource["kind"].(string)
	if !ok {
		return false
	}
	return kind == "Secret" || kind == "SecretList"
}

// isKubernetesList checks if a resource map represents a Kubernetes List.
func isKubernetesList(resource map[string]any) bool {
	kind, ok := resource["kind"].(string)
	if !ok {
		return false
	}
	return kind == "List" || strings.HasSuffix(kind, "List")
}

// maskSecretFields replaces values in "data" and "stringData" fields with the masked placeholder.
func maskSecretFields(resource map[string]any) {
	// Handle SecretList with items
	if kind, _ := resource["kind"].(string); kind == "SecretList" {
		if items, ok := resource["items"]; ok {
			if itemList, ok := items.([]any); ok {
				for _, item := range itemList {
					if itemMap, ok := item.(map[string]any); ok {
						maskSecretDataMaps(itemMap)
					}
				}
			}
		}
		return
	}

	maskSecretDataMaps(resource)
}

// maskSecretDataMaps replaces values in "data" and "stringData" map fields.
func maskSecretDataMaps(resource map[string]any) {
	for _, field := range []string{"data", "stringData"} {
		fieldVal, ok := resource[field]
		if !ok {
			continue
		}

		dataMap, ok := fieldVal.(map[string]any)
		if !ok {
			continue
		}

		for key := range dataMap {
			dataMap[key] = MaskedSecretValue
		}
	}
}

// maskAnnotationSecrets checks annotations for embedded JSON containing Secret data.
// For example, kubectl.kubernetes.io/last-applied-configuration often contains
// a JSON representation of the Secret.
func maskAnnotationSecrets(resource map[string]any) {
	metadata, ok := resource["metadata"].(map[string]any)
	if !ok {
		return
	}

	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		return
	}

	for key, val := range annotations {
		strVal, ok := val.(string)
		if !ok || !strings.Contains(strVal, "Secret") {
			continue
		}

		// Try to parse the annotation value as JSON
		var embedded map[string]any
		if err := json.Unmarshal([]byte(strVal), &embedded); err != nil {
			continue
		}

		if isKubernetesSecret(embedded) {
			maskSecretFields(embedded)
			// Re-serialize
			masked, err := json.Marshal(embedded)
			if err != nil {
				continue
			}
			annotations[key] = string(masked)
		}
	}
}
