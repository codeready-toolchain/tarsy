package masking

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readTestdata(t *testing.T, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", filename))
	require.NoError(t, err)
	return string(data)
}

func TestKubernetesSecretMasker_Name(t *testing.T) {
	m := &KubernetesSecretMasker{}
	assert.Equal(t, "kubernetes_secret", m.Name())
}

func TestKubernetesSecretMasker_AppliesTo(t *testing.T) {
	m := &KubernetesSecretMasker{}

	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{
			name:   "YAML Secret",
			input:  "apiVersion: v1\nkind: Secret\nmetadata:\n  name: test",
			expect: true,
		},
		{
			name:   "JSON Secret",
			input:  `{"apiVersion": "v1", "kind": "Secret", "metadata": {"name": "test"}}`,
			expect: true,
		},
		{
			name:   "YAML SecretList",
			input:  "apiVersion: v1\nkind: SecretList\nitems: []",
			expect: true,
		},
		{
			name:   "JSON SecretList",
			input:  `{"apiVersion": "v1", "kind": "SecretList", "items": []}`,
			expect: true,
		},
		{
			name:   "ConfigMap",
			input:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test",
			expect: false,
		},
		{
			name:   "No Secret keyword",
			input:  "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test",
			expect: false,
		},
		{
			name:   "Secret in text but not as kind",
			input:  "This is a Secret message about something",
			expect: false,
		},
		{
			name:   "Empty string",
			input:  "",
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, m.AppliesTo(tt.input))
		})
	}
}

func TestKubernetesSecretMasker_YAML_SingleSecret(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := readTestdata(t, "secret_yaml.txt")

	result := m.Mask(input)

	assert.NotEqual(t, input, result, "Should have masked the secret")
	assert.Contains(t, result, MaskedSecretValue)
	assert.Contains(t, result, "kind: Secret", "Kind should be preserved")
	assert.Contains(t, result, "name: test-fake-secret", "Metadata should be preserved")
	assert.NotContains(t, result, "RkFLRS1hZG1pbg==", "Base64 data should be masked")
	assert.NotContains(t, result, "RkFLRS1wYXNzd29yZA==", "Base64 data should be masked")
	assert.NotContains(t, result, "FAKE-api-key-not-real", "stringData should be masked")
}

func TestKubernetesSecretMasker_YAML_ConfigMap_NotMasked(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := readTestdata(t, "configmap_yaml.txt")

	// ConfigMap should not trigger AppliesTo
	assert.False(t, m.AppliesTo(input))

	// If called directly, Mask should return original
	result := m.Mask(input)
	assert.Equal(t, input, result, "ConfigMap should NOT be masked")
}

func TestKubernetesSecretMasker_YAML_MultiDocument(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := readTestdata(t, "secret_list_yaml.txt")

	result := m.Mask(input)

	assert.NotEqual(t, input, result, "Should have masked secrets")

	// Secrets should be masked
	assert.NotContains(t, result, "RkFLRS1kYi1wYXNz", "Secret data should be masked")
	assert.NotContains(t, result, "RkFLRS10bHMtY2VydC1kYXRh", "TLS cert data should be masked")

	// ConfigMap should NOT be masked
	assert.Contains(t, result, "production", "ConfigMap values should be preserved")
	assert.Contains(t, result, "kind: ConfigMap", "ConfigMap kind should be preserved")
	assert.Contains(t, result, "APP_ENV", "ConfigMap keys should be preserved")
}

func TestKubernetesSecretMasker_JSON_SingleSecret(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := readTestdata(t, "secret_json.txt")

	result := m.Mask(input)

	assert.NotEqual(t, input, result, "Should have masked the secret")
	assert.Contains(t, result, MaskedSecretValue)
	assert.Contains(t, result, `"kind": "Secret"`, "Kind should be preserved")
	assert.NotContains(t, result, "RkFLRS1hZG1pbg==", "Base64 data should be masked")
	assert.NotContains(t, result, "RkFLRS1wYXNzd29yZA==", "Base64 data should be masked")
	assert.NotContains(t, result, "FAKE-api-key-not-real", "stringData should be masked")
}

func TestKubernetesSecretMasker_JSON_List(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := readTestdata(t, "mixed_resources.txt")

	result := m.Mask(input)

	assert.NotEqual(t, input, result, "Should have masked secrets")

	// Parse result to verify structure
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))

	rawItems, ok := parsed["items"].([]any)
	require.True(t, ok, "items should be an array")
	require.Len(t, rawItems, 3)

	// First item: Secret — should be masked (entire data section replaced)
	secret1, ok := rawItems[0].(map[string]any)
	require.True(t, ok, "item 0 should be a map")
	assert.Equal(t, "Secret", secret1["kind"])
	assert.Equal(t, MaskedSecretValue, secret1["data"])

	// Second item: ConfigMap — should NOT be masked
	configMap, ok := rawItems[1].(map[string]any)
	require.True(t, ok, "item 1 should be a map")
	assert.Equal(t, "ConfigMap", configMap["kind"])
	cmData, ok := configMap["data"].(map[string]any)
	require.True(t, ok, "item 1 data should be a map")
	assert.Equal(t, "staging", cmData["ENVIRONMENT"])
	assert.Equal(t, "false", cmData["DEBUG"])

	// Third item: Secret — should be masked (entire data section replaced)
	secret2, ok := rawItems[2].(map[string]any)
	require.True(t, ok, "item 2 should be a map")
	assert.Equal(t, "Secret", secret2["kind"])
	assert.Equal(t, MaskedSecretValue, secret2["data"])
}

func TestKubernetesSecretMasker_MalformedYAML(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := "kind: Secret\nthis is not: valid: yaml: [["

	result := m.Mask(input)
	assert.Equal(t, input, result, "Malformed YAML should return original")
}

func TestKubernetesSecretMasker_MalformedJSON(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := `{"kind": "Secret", "data": {broken json`

	result := m.Mask(input)
	assert.Equal(t, input, result, "Malformed JSON should return original")
}

func TestKubernetesSecretMasker_EmptyDataField(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := `apiVersion: v1
kind: Secret
metadata:
  name: empty-secret
data: {}
`
	result := m.Mask(input)

	// Even empty data section should be replaced with placeholder
	assert.Contains(t, result, "kind: Secret")
	assert.Contains(t, result, MaskedSecretValue)
}

func TestKubernetesSecretMasker_StringDataField(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := `apiVersion: v1
kind: Secret
metadata:
  name: test-fake-string-secret
stringData:
  username: FAKE-user-not-real
  password: FAKE-pass-not-real
`
	result := m.Mask(input)

	assert.Contains(t, result, MaskedSecretValue)
	assert.NotContains(t, result, "FAKE-user-not-real")
	assert.NotContains(t, result, "FAKE-pass-not-real")
}

func TestKubernetesSecretMasker_AnnotationWithEmbeddedJSON(t *testing.T) {
	m := &KubernetesSecretMasker{}
	embeddedJSON := `{"apiVersion":"v1","kind":"Secret","data":{"password":"RkFLRS1wd2Q="}}`
	input := `apiVersion: v1
kind: Secret
metadata:
  name: test-fake-annotated-secret
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: '` + embeddedJSON + `'
data:
  password: RkFLRS1wd2Q=
`
	result := m.Mask(input)

	assert.Contains(t, result, MaskedSecretValue)
	assert.NotContains(t, result, "RkFLRS1wd2Q=")

	// The annotation value should also have masked data
	assert.NotContains(t, result, `"password":"RkFLRS1wd2Q="`)
}

func TestKubernetesSecretMasker_NoDataOrStringData(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := `apiVersion: v1
kind: Secret
metadata:
  name: no-data-secret
type: Opaque
`
	result := m.Mask(input)

	// Should not error, just process normally
	assert.Contains(t, result, "kind: Secret")
}

func TestKubernetesSecretMasker_JSONSecretList(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := `{
  "apiVersion": "v1",
  "kind": "SecretList",
  "items": [
    {
      "apiVersion": "v1",
      "kind": "Secret",
      "metadata": {"name": "test-fake-secret-1"},
      "data": {"key1": "RkFLRS12YWwx"}
    },
    {
      "apiVersion": "v1",
      "kind": "Secret",
      "metadata": {"name": "test-fake-secret-2"},
      "data": {"key2": "RkFLRS12YWwy"}
    }
  ]
}`

	result := m.Mask(input)

	assert.NotEqual(t, input, result)
	assert.NotContains(t, result, "RkFLRS12YWwx")
	assert.NotContains(t, result, "RkFLRS12YWwy")
	assert.Contains(t, result, MaskedSecretValue)

	// Parse to verify structure
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))

	rawItems, ok := parsed["items"].([]any)
	require.True(t, ok, "items should be an array")
	require.Len(t, rawItems, 2)

	for i, item := range rawItems {
		itemMap, ok := item.(map[string]any)
		require.True(t, ok, "item %d should be a map", i)
		assert.Equal(t, MaskedSecretValue, itemMap["data"], "item %d data should be fully masked", i)
	}
}

func TestKubernetesSecretMasker_YAMLSecretList(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := `apiVersion: v1
kind: SecretList
items:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: test-fake-secret-a
    data:
      key: RkFLRS1rZXlB
  - apiVersion: v1
    kind: Secret
    metadata:
      name: test-fake-secret-b
    data:
      key: RkFLRS1rZXlC
`
	result := m.Mask(input)

	assert.NotEqual(t, input, result)
	assert.NotContains(t, result, "RkFLRS1rZXlB")
	assert.NotContains(t, result, "RkFLRS1rZXlC")
	assert.Contains(t, result, MaskedSecretValue)
}

func TestKubernetesSecretMasker_SecretListAnnotationsMasked(t *testing.T) {
	// Verify that annotations on individual items inside a SecretList are masked.
	// This is the key behavior that requires SecretList to go through maskListItems
	// (via isKubernetesList) rather than isKubernetesSecret.
	m := &KubernetesSecretMasker{}
	input := `{
  "apiVersion": "v1",
  "kind": "SecretList",
  "items": [
    {
      "apiVersion": "v1",
      "kind": "Secret",
      "metadata": {
        "name": "test-fake-annotated",
        "annotations": {
          "kubectl.kubernetes.io/last-applied-configuration": "{\"apiVersion\":\"v1\",\"kind\":\"Secret\",\"data\":{\"pw\":\"RkFLRS1wd2Q=\"}}"
        }
      },
      "data": {"token": "RkFLRS10b2tlbg=="}
    }
  ]
}`

	result := m.Mask(input)

	assert.NotEqual(t, input, result)
	// Data fields should be masked
	assert.NotContains(t, result, "RkFLRS10b2tlbg==", "Item data should be masked")
	// Embedded annotation JSON should also be masked
	assert.NotContains(t, result, "RkFLRS1wd2Q=", "Annotation embedded Secret data should be masked")
	assert.Contains(t, result, MaskedSecretValue)
}

func TestKubernetesSecretMasker_PreservesNonSecretContent(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := `apiVersion: v1
kind: Secret
metadata:
  name: test-fake-labeled-secret
  namespace: default
  labels:
    app: myapp
    tier: backend
type: Opaque
data:
  password: RkFLRS1wYXNz
`
	result := m.Mask(input)

	// Labels and metadata should be preserved
	assert.Contains(t, result, "app: myapp")
	assert.Contains(t, result, "tier: backend")
	assert.Contains(t, result, "namespace: default")
	assert.Contains(t, result, "type: Opaque")

	// Data should be masked
	assert.NotContains(t, result, "RkFLRS1wYXNz")
	assert.Contains(t, result, MaskedSecretValue)
}

func TestKubernetesSecretMasker_PlainTextNotAffected(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := "This is just plain text mentioning kind: Secret in a log message"

	// AppliesTo should return true (it sees the pattern)
	if m.AppliesTo(input) {
		result := m.Mask(input)
		// Should return original since it's not valid YAML or JSON
		assert.Equal(t, input, result)
	}
}

func TestIsKubernetesSecret(t *testing.T) {
	tests := []struct {
		name     string
		resource map[string]any
		expect   bool
	}{
		{
			name:     "Secret",
			resource: map[string]any{"kind": "Secret"},
			expect:   true,
		},
		{
			name:     "SecretList is not a Secret (handled as List)",
			resource: map[string]any{"kind": "SecretList"},
			expect:   false,
		},
		{
			name:     "ConfigMap",
			resource: map[string]any{"kind": "ConfigMap"},
			expect:   false,
		},
		{
			name:     "Pod",
			resource: map[string]any{"kind": "Pod"},
			expect:   false,
		},
		{
			name:     "no kind",
			resource: map[string]any{"apiVersion": "v1"},
			expect:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, isKubernetesSecret(tt.resource))
		})
	}
}

func TestIsKubernetesList(t *testing.T) {
	tests := []struct {
		name     string
		resource map[string]any
		expect   bool
	}{
		{
			name:     "List",
			resource: map[string]any{"kind": "List"},
			expect:   true,
		},
		{
			name:     "SecretList",
			resource: map[string]any{"kind": "SecretList"},
			expect:   true,
		},
		{
			name:     "ConfigMapList",
			resource: map[string]any{"kind": "ConfigMapList"},
			expect:   true,
		},
		{
			name:     "Secret",
			resource: map[string]any{"kind": "Secret"},
			expect:   false,
		},
		{
			name:     "no kind",
			resource: map[string]any{},
			expect:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, isKubernetesList(tt.resource))
		})
	}
}

func TestMaskSecretFields(t *testing.T) {
	resource := map[string]any{
		"kind": "Secret",
		"data": map[string]any{
			"username": "RkFLRS11c2Vy",
			"password": "RkFLRS1wYXNz",
		},
		"stringData": map[string]any{
			"api-key": "FAKE-key-not-real",
		},
	}

	maskSecretFields(resource)

	// Entire data and stringData sections should be replaced with the placeholder
	assert.Equal(t, MaskedSecretValue, resource["data"])
	assert.Equal(t, MaskedSecretValue, resource["stringData"])
}

func TestMaskAnnotationSecrets(t *testing.T) {
	t.Run("masks embedded JSON Secret in annotation", func(t *testing.T) {
		resource := map[string]any{
			"kind": "Secret",
			"metadata": map[string]any{
				"name": "test",
				"annotations": map[string]any{
					"kubectl.kubernetes.io/last-applied-configuration": `{"kind":"Secret","data":{"pw":"RkFLRS1wd2Q="}}`,
				},
			},
		}

		maskAnnotationSecrets(resource)

		annotations := resource["metadata"].(map[string]any)["annotations"].(map[string]any)
		annotationVal := annotations["kubectl.kubernetes.io/last-applied-configuration"].(string)
		assert.NotContains(t, annotationVal, "RkFLRS1wd2Q=")
		assert.Contains(t, annotationVal, MaskedSecretValue)
	})

	t.Run("skips non-Secret annotations", func(t *testing.T) {
		resource := map[string]any{
			"kind": "ConfigMap",
			"metadata": map[string]any{
				"annotations": map[string]any{
					"some-annotation": `{"kind":"ConfigMap","data":{"key":"value"}}`,
				},
			},
		}

		maskAnnotationSecrets(resource)

		annotations := resource["metadata"].(map[string]any)["annotations"].(map[string]any)
		assert.Contains(t, annotations["some-annotation"].(string), "value")
	})

	t.Run("skips non-JSON annotations", func(t *testing.T) {
		resource := map[string]any{
			"kind": "Secret",
			"metadata": map[string]any{
				"annotations": map[string]any{
					"description": "Contains Secret info but is not JSON",
				},
			},
		}

		maskAnnotationSecrets(resource)

		annotations := resource["metadata"].(map[string]any)["annotations"].(map[string]any)
		assert.Equal(t, "Contains Secret info but is not JSON", annotations["description"])
	})
}

func TestKubernetesSecretMasker_JSON_PreservesFormatting(t *testing.T) {
	m := &KubernetesSecretMasker{}
	input := `{"apiVersion":"v1","kind":"Secret","data":{"pw":"RkFLRS1wdw=="}}`

	result := m.Mask(input)

	assert.NotEqual(t, input, result)
	assert.Contains(t, result, MaskedSecretValue)
	assert.NotContains(t, result, "RkFLRS1wdw==")

	// Result should be valid JSON
	var parsed map[string]any
	assert.NoError(t, json.Unmarshal([]byte(result), &parsed))
}

func TestKubernetesSecretMasker_YAML_NoSecret(t *testing.T) {
	m := &KubernetesSecretMasker{}
	// AppliesTo returns true (has "Secret" in kind field via regex), but kind is actually something else
	input := `apiVersion: v1
kind: SecretStore
metadata:
  name: not-a-secret
`
	// Even though "SecretStore" contains "Secret", yamlSecretPattern only matches "Secret" or "SecretList"
	assert.False(t, m.AppliesTo(input), "Should not apply to SecretStore")
}

func TestKubernetesSecretMasker_FullLifecycle(t *testing.T) {
	// Verify the full AppliesTo → Mask lifecycle against a real test fixture
	m := &KubernetesSecretMasker{}

	input := readTestdata(t, "secret_yaml.txt")

	assert.True(t, m.AppliesTo(input))
	result := m.Mask(input)

	assert.NotEqual(t, input, result)
	assert.Contains(t, result, MaskedSecretValue)
	assert.Contains(t, result, "kind: Secret")
	assert.NotContains(t, result, "RkFLRS1hZG1pbg==")
	assert.NotContains(t, result, "RkFLRS1wYXNzd29yZA==")
	assert.NotContains(t, result, "FAKE-api-key-not-real")

	// Verify metadata is fully preserved
	assert.True(t, strings.Contains(result, "name: test-fake-secret") ||
		strings.Contains(result, "name: \"test-fake-secret\""))
}
