package config

import (
	"sync"
)

// BuiltinConfig holds all built-in configuration data.
// This provides default agents, MCP servers, LLM providers, chains, and masking patterns.
type BuiltinConfig struct {
	Agents           map[string]BuiltinAgentConfig
	MCPServers       map[string]MCPServerConfig
	LLMProviders     map[string]LLMProviderConfig
	ChainDefinitions map[string]ChainConfig
	MaskingPatterns  map[string]MaskingPattern
	PatternGroups    map[string][]string
	CodeMaskers      map[string]string
	DefaultRunbook   string
	DefaultAlertType string
}

// BuiltinAgentConfig holds built-in agent metadata (configuration only)
// Agent instantiation/factory pattern is handled in Phase 3: Agent Framework
type BuiltinAgentConfig struct {
	Description       string
	IterationStrategy IterationStrategy
	MCPServers        []string
}

var (
	builtinConfig     *BuiltinConfig
	builtinConfigOnce sync.Once
)

// GetBuiltinConfig returns the singleton built-in configuration (thread-safe, lazy-initialized)
func GetBuiltinConfig() *BuiltinConfig {
	builtinConfigOnce.Do(initBuiltinConfig)
	return builtinConfig
}

func initBuiltinConfig() {
	builtinConfig = &BuiltinConfig{
		Agents:           initBuiltinAgents(),
		MCPServers:       initBuiltinMCPServers(),
		LLMProviders:     initBuiltinLLMProviders(),
		ChainDefinitions: initBuiltinChains(),
		MaskingPatterns:  initBuiltinMaskingPatterns(),
		PatternGroups:    initBuiltinPatternGroups(),
		CodeMaskers:      initBuiltinCodeMaskers(),
		DefaultRunbook:   defaultRunbookContent,
		DefaultAlertType: "kubernetes",
	}
}

func initBuiltinAgents() map[string]BuiltinAgentConfig {
	return map[string]BuiltinAgentConfig{
		"KubernetesAgent": {
			Description:       "Kubernetes-specialized agent using ReAct pattern",
			IterationStrategy: IterationStrategyReact,
			MCPServers:        []string{"kubernetes-server"},
		},
		"ChatAgent": {
			Description:       "Built-in agent for follow-up conversations",
			IterationStrategy: IterationStrategyReact,
			MCPServers:        []string{"kubernetes-server"},
		},
		"SynthesisAgent": {
			Description:       "Synthesizes parallel investigation results",
			IterationStrategy: IterationStrategySynthesis,
			MCPServers:        []string{"kubernetes-server"}, // Needs at least one server for validation
		},
	}
}

func initBuiltinMCPServers() map[string]MCPServerConfig {
	return map[string]MCPServerConfig{
		"kubernetes-server": {
			Transport: TransportConfig{
				Type:    TransportTypeStdio,
				Command: "npx",
				Args: []string{
					"-y",
					"kubernetes-mcp-server@0.0.54",
					"--read-only",
					"--disable-destructive",
					"--kubeconfig",
					"${KUBECONFIG}",
				},
			},
			Instructions: `For Kubernetes operations:
- **IMPORTANT: In multi-cluster environments** (when the 'configuration_contexts_list' tool is available):
  * ALWAYS start by calling 'configuration_contexts_list' to see all available contexts and their server URLs
  * Use this information to determine which context to target before performing any operations
  * This prevents working on the wrong cluster and helps you understand the environment
- Be careful with cluster-scoped resource listings in large clusters
- Always prefer namespaced queries when possible
- If you get "server could not find the requested resource" error, check if you're using the namespace parameter correctly:
  * Cluster-scoped resources (Namespace, Node, ClusterRole, PersistentVolume) should NOT have a namespace parameter
  * Namespace-scoped resources (Pod, Deployment, Service, ConfigMap) REQUIRE a namespace parameter`,
			DataMasking: &MaskingConfig{
				Enabled:       true,
				PatternGroups: []string{"kubernetes"},
				Patterns:      []string{"certificate", "token", "email"},
			},
			Summarization: &SummarizationConfig{
				Enabled:              true,
				SizeThresholdTokens:  5000,
				SummaryMaxTokenLimit: 1000,
			},
		},
	}
}

func initBuiltinLLMProviders() map[string]LLMProviderConfig {
	return map[string]LLMProviderConfig{
		"google-default": {
			Type:                LLMProviderTypeGoogle,
			Model:               "gemini-2.5-pro",
			APIKeyEnv:           "GOOGLE_API_KEY",
			MaxToolResultTokens: 950000, // Conservative for 1M context
			NativeTools: map[GoogleNativeTool]bool{
				GoogleNativeToolGoogleSearch:  true,
				GoogleNativeToolCodeExecution: false, // Disabled by default
				GoogleNativeToolURLContext:    true,
			},
		},
		"openai-default": {
			Type:                LLMProviderTypeOpenAI,
			Model:               "gpt-5",
			APIKeyEnv:           "OPENAI_API_KEY",
			MaxToolResultTokens: 250000, // Conservative for 272K context
		},
		"anthropic-default": {
			Type:                LLMProviderTypeAnthropic,
			Model:               "claude-sonnet-4-20250514",
			APIKeyEnv:           "ANTHROPIC_API_KEY",
			MaxToolResultTokens: 150000, // Conservative for 200K context
		},
		"xai-default": {
			Type:                LLMProviderTypeXAI,
			Model:               "grok-4",
			APIKeyEnv:           "XAI_API_KEY",
			MaxToolResultTokens: 200000, // Conservative for 256K context
		},
		"vertexai-default": {
			Type:                LLMProviderTypeVertexAI,
			Model:               "claude-sonnet-4-5@20250929", // Claude Sonnet 4.5 on Vertex AI
			ProjectEnv:          "GOOGLE_CLOUD_PROJECT",       // Standard GCP project ID env var
			LocationEnv:         "GOOGLE_CLOUD_LOCATION",      // Standard GCP location env var
			MaxToolResultTokens: 150000,                       // Conservative for 200K context
		},
	}
}

func initBuiltinChains() map[string]ChainConfig {
	return map[string]ChainConfig{
		"kubernetes-agent-chain": {
			AlertTypes:  []string{"kubernetes"},
			Description: "Single-stage Kubernetes analysis",
			Stages: []StageConfig{
				{
					Name: "analysis",
					Agents: []StageAgentConfig{
						{Name: "KubernetesAgent"},
					},
				},
			},
		},
	}
}

func initBuiltinMaskingPatterns() map[string]MaskingPattern {
	return map[string]MaskingPattern{
		"api_key": {
			Pattern:     `(?i)(?:api[_-]?key|apikey|key)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-]{20,})["\']?`,
			Replacement: `"api_key": "__MASKED_API_KEY__"`,
			Description: "API keys",
		},
		"password": {
			Pattern:     `(?i)(?:password|pwd|pass)["\']?\s*[:=]\s*["\']?([^"\'\s\n]{6,})["\']?`,
			Replacement: `"password": "__MASKED_PASSWORD__"`,
			Description: "Passwords",
		},
		"certificate": {
			Pattern:     `(?s)-----BEGIN [A-Z ]+-----.*?-----END [A-Z ]+-----`,
			Replacement: `__MASKED_CERTIFICATE__`,
			Description: "SSL/TLS certificates",
		},
		"certificate_authority_data": {
			Pattern:     `(?i)certificate-authority-data:\s*([A-Za-z0-9+/]{20,}={0,2})`,
			Replacement: `certificate-authority-data: __MASKED_CA_CERTIFICATE__`,
			Description: "K8s CA data",
		},
		"token": {
			Pattern:     `(?i)(?:token|bearer|jwt)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-\.]{20,})["\']?`,
			Replacement: `"token": "__MASKED_TOKEN__"`,
			Description: "Access tokens",
		},
		"email": {
			Pattern:     `\b[A-Za-z0-9._%+-]+@[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*\.[A-Za-z]{2,63}\b`,
			Replacement: `__MASKED_EMAIL__`,
			Description: "Email addresses",
		},
		"ssh_key": {
			Pattern:     `ssh-(?:rsa|dss|ed25519|ecdsa)\s+[A-Za-z0-9+/=]+`,
			Replacement: `__MASKED_SSH_KEY__`,
			Description: "SSH public keys",
		},
		"base64_secret": {
			Pattern:     `\b([A-Za-z0-9+/]{20,}={0,2})\b`,
			Replacement: `__MASKED_BASE64_VALUE__`,
			Description: "Base64 values (20+ chars)",
		},
		"base64_short": {
			Pattern:     `:\s+([A-Za-z0-9+/]{4,19}={0,2})(?:\s|$)`,
			Replacement: `: __MASKED_SHORT_BASE64__`,
			Description: "Short base64 values",
		},
		"private_key": {
			Pattern:     `(?i)(?:private[_-]?key)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-\.]{20,})["\']?`,
			Replacement: `"private_key": "__MASKED_PRIVATE_KEY__"`,
			Description: "Private keys",
		},
		"secret_key": {
			Pattern:     `(?i)(?:secret[_-]?key)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-\.]{20,})["\']?`,
			Replacement: `"secret_key": "__MASKED_SECRET_KEY__"`,
			Description: "Secret keys",
		},
		"aws_access_key": {
			Pattern:     `(?i)(?:aws[_-]?access[_-]?key[_-]?id)["\']?\s*[:=]\s*["\']?(AKIA[A-Z0-9]{16})["\']?`,
			Replacement: `"aws_access_key_id": "__MASKED_AWS_KEY__"`,
			Description: "AWS access keys",
		},
		"aws_secret_key": {
			Pattern:     `(?i)(?:aws[_-]?secret[_-]?access[_-]?key)["\']?\s*[:=]\s*["\']?([A-Za-z0-9/+=]{40})["\']?`,
			Replacement: `"aws_secret_access_key": "__MASKED_AWS_SECRET__"`,
			Description: "AWS secret keys",
		},
		"github_token": {
			Pattern:     `(?i)(?:github[_-]?token|gh[ps]_[A-Za-z0-9_]{36,255})`,
			Replacement: `__MASKED_GITHUB_TOKEN__`,
			Description: "GitHub tokens",
		},
		"slack_token": {
			Pattern:     `(?i)xox[baprs]-[A-Za-z0-9-]{10,72}`,
			Replacement: `__MASKED_SLACK_TOKEN__`,
			Description: "Slack tokens",
		},
	}
}

// initBuiltinPatternGroups returns predefined groups of masking patterns.
// Pattern group members can reference either:
//   - MaskingPatterns: regex-based patterns (implemented)
//   - CodeMaskers: code-based maskers for complex structural parsing (TODO - Phase 7)
//
// Example: "kubernetes_secret" is a code-based masker that parses YAML/JSON
// to mask only Secret data (not ConfigMaps), so it appears in CodeMaskers
// instead of MaskingPatterns. This is a placeholder for Phase 7 implementation.
func initBuiltinPatternGroups() map[string][]string {
	return map[string][]string{
		"basic":      {"api_key", "password"},                                                                                                                                                                                                            // Most common secrets
		"secrets":    {"api_key", "password", "token", "private_key", "secret_key"},                                                                                                                                                                      // Basic + tokens
		"security":   {"api_key", "password", "token", "certificate", "certificate_authority_data", "email", "ssh_key"},                                                                                                                                  // Full security focus
		"kubernetes": {"kubernetes_secret", "api_key", "password", "certificate_authority_data"},                                                                                                                                                         // Kubernetes-specific - kubernetes_secret is TODO (Phase 7), others are regex-based
		"cloud":      {"aws_access_key", "aws_secret_key", "api_key", "token"},                                                                                                                                                                           // Cloud provider secrets
		"all":        {"base64_secret", "base64_short", "api_key", "password", "certificate", "certificate_authority_data", "email", "token", "ssh_key", "private_key", "secret_key", "aws_access_key", "aws_secret_key", "github_token", "slack_token"}, // All patterns
	}
}

// initBuiltinCodeMaskers returns code-based maskers for complex masking scenarios.
// These maskers require structural parsing and can be referenced in PatternGroups.
// Unlike regex patterns in MaskingPatterns, code-based maskers implement custom logic.
//
// Example: kubernetes_secret masker parses YAML/JSON to distinguish between
// Kubernetes Secrets (which should be masked) and ConfigMaps (which should not).
// This masker is referenced in the "kubernetes" pattern group.
//
// TODO: Code-based maskers are not yet implemented - they are placeholders for Phase 7.
// See docs/project-plan.md Phase 7: Security & Data → Data Masking
func initBuiltinCodeMaskers() map[string]string {
	return map[string]string{
		// TODO (Phase 7): Implement KubernetesSecretMasker
		// This masker needs to:
		//   1. Parse YAML/JSON structures from MCP tool results
		//   2. Identify Kubernetes Secret resources (kind: Secret)
		//   3. Mask Secret.data fields (base64-encoded sensitive data)
		//   4. Leave ConfigMaps untouched (kind: ConfigMap)
		//   5. Handle both single resources and lists (e.g., kubectl get secrets -o yaml)
		// Referenced in: "kubernetes" pattern group
		// Implementation path: pkg/maskers/kubernetes_secret.go
		"kubernetes_secret": "pkg/maskers.KubernetesSecretMasker",
	}
}

const defaultRunbookContent = `# Generic Troubleshooting Guide

## Investigation Steps

1. **Analyze the alert** - Review alert data and identify affected system/service
2. **Gather context** - Use tools to check current state and recent changes
3. **Identify root cause** - Investigate potential causes based on alert type
4. **Assess impact** - Determine scope and severity
5. **Recommend actions** - Suggest safe investigation or remediation steps

## Guidelines

- Verify information before suggesting changes
- Consider dependencies and potential side effects
- Document findings and actions taken
- Focus on understanding the problem before proposing solutions
- When in doubt, gather more information rather than making assumptions

## Common Investigation Patterns

### For Performance Issues
- Check resource utilization (CPU, memory, disk, network)
- Review recent deployments or configuration changes
- Analyze metrics and logs for anomalies
- Identify bottlenecks in the request path

### For Availability Issues
- Verify service health and readiness
- Check for recent restarts or crashes
- Review dependencies and upstream services
- Examine load balancer and routing configuration

### For Error Rate Spikes
- Analyze error messages and stack traces
- Correlate with recent deployments
- Check for external service failures
- Review input validation and edge cases
`
