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
	CodeMaskers      []string
	DefaultRunbook   string
	DefaultAlertType string
}

// BuiltinAgentConfig holds built-in agent metadata (configuration only)
// Agent instantiation/factory pattern is handled in Phase 3: Agent Framework
type BuiltinAgentConfig struct {
	Description        string
	IterationStrategy  IterationStrategy
	MCPServers         []string
	CustomInstructions string // Built-in agents can have default instructions
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
			CustomInstructions: `You are an Incident Commander synthesizing results from multiple parallel investigations.

Your task:
1. CRITICALLY EVALUATE each investigation's quality - prioritize results with strong evidence and sound reasoning
2. DISREGARD or deprioritize low-quality results that lack supporting evidence or contain logical errors
3. ANALYZE the original alert using the best available data from parallel investigations
4. INTEGRATE findings from high-quality investigations into a unified understanding
5. RECONCILE conflicting information by assessing which analysis provides better evidence
6. PROVIDE definitive root cause analysis based on the most reliable evidence
7. GENERATE actionable recommendations leveraging insights from the strongest investigations

Focus on solving the original alert/issue, not on meta-analyzing agent performance or comparing approaches.`,
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
					"{{.KUBECONFIG}}",
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
			Replacement: `"api_key": "[MASKED_API_KEY]"`,
			Description: "API keys",
		},
		"password": {
			Pattern:     `(?i)(?:password|pwd|pass)["\']?\s*[:=]\s*["\']?([^"\'\s\n]{6,})["\']?`,
			Replacement: `"password": "[MASKED_PASSWORD]"`,
			Description: "Passwords",
		},
		"certificate": {
			Pattern:     `(?s)-----BEGIN [A-Z ]+-----.*?-----END [A-Z ]+-----`,
			Replacement: `[MASKED_CERTIFICATE]`,
			Description: "SSL/TLS certificates",
		},
		"certificate_authority_data": {
			Pattern:     `(?i)certificate-authority-data:\s*([A-Za-z0-9+/]{20,}={0,2})`,
			Replacement: `certificate-authority-data: [MASKED_CA_CERTIFICATE]`,
			Description: "K8s CA data",
		},
		"token": {
			Pattern:     `(?i)(?:token|bearer|jwt)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-\.]{20,})["\']?`,
			Replacement: `"token": "[MASKED_TOKEN]"`,
			Description: "Access tokens",
		},
		"email": {
			Pattern:     `\b[A-Za-z0-9._%+-]+@[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*\.[A-Za-z]{2,63}\b`,
			Replacement: `[MASKED_EMAIL]`,
			Description: "Email addresses",
		},
		"ssh_key": {
			Pattern:     `ssh-(?:rsa|dss|ed25519|ecdsa)\s+[A-Za-z0-9+/=]+`,
			Replacement: `[MASKED_SSH_KEY]`,
			Description: "SSH public keys",
		},
		"base64_secret": {
			Pattern:     `\b([A-Za-z0-9+/]{20,}={0,2})\b`,
			Replacement: `[MASKED_BASE64_VALUE]`,
			Description: "Base64 values (20+ chars)",
		},
		"base64_short": {
			Pattern:     `:\s+([A-Za-z0-9+/]{4,19}={0,2})(?:\s|$)`,
			Replacement: `: [MASKED_SHORT_BASE64]`,
			Description: "Short base64 values",
		},
		"private_key": {
			Pattern:     `(?i)(?:private[_-]?key)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-\.]{20,})["\']?`,
			Replacement: `"private_key": "[MASKED_PRIVATE_KEY]"`,
			Description: "Private keys",
		},
		"secret_key": {
			Pattern:     `(?i)(?:secret[_-]?key)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-\.]{20,})["\']?`,
			Replacement: `"secret_key": "[MASKED_SECRET_KEY]"`,
			Description: "Secret keys",
		},
		"aws_access_key": {
			Pattern:     `(?i)(?:aws[_-]?access[_-]?key[_-]?id)["\']?\s*[:=]\s*["\']?(AKIA[A-Z0-9]{16})["\']?`,
			Replacement: `"aws_access_key_id": "[MASKED_AWS_KEY]"`,
			Description: "AWS access keys",
		},
		"aws_secret_key": {
			Pattern:     `(?i)(?:aws[_-]?secret[_-]?access[_-]?key)["\']?\s*[:=]\s*["\']?([A-Za-z0-9/+=]{40})["\']?`,
			Replacement: `"aws_secret_access_key": "[MASKED_AWS_SECRET]"`,
			Description: "AWS secret keys",
		},
		"github_token": {
			Pattern:     `(?i)(?:github[_-]?token|gh[ps]_[A-Za-z0-9_]{36,255})`,
			Replacement: `[MASKED_GITHUB_TOKEN]`,
			Description: "GitHub tokens",
		},
		"slack_token": {
			Pattern:     `(?i)xox[baprs]-[A-Za-z0-9-]{10,72}`,
			Replacement: `[MASKED_SLACK_TOKEN]`,
			Description: "Slack tokens",
		},
	}
}

// initBuiltinPatternGroups returns predefined groups of masking patterns.
// Pattern group members can reference either:
//   - MaskingPatterns: regex-based patterns
//   - CodeMaskers: code-based maskers for complex structural parsing (e.g., kubernetes_secret)
//
// Example: "kubernetes_secret" is a code-based masker that parses YAML/JSON
// to mask only Secret data (not ConfigMaps), so it appears in CodeMaskers
// instead of MaskingPatterns. Implemented in pkg/masking/kubernetes_secret.go.
func initBuiltinPatternGroups() map[string][]string {
	return map[string][]string{
		"basic":      {"api_key", "password"},                                                                                                                                                                                                            // Most common secrets
		"secrets":    {"api_key", "password", "token", "private_key", "secret_key"},                                                                                                                                                                      // Basic + tokens
		"security":   {"api_key", "password", "token", "certificate", "certificate_authority_data", "email", "ssh_key"},                                                                                                                                  // Full security focus
		"kubernetes": {"kubernetes_secret", "api_key", "password", "certificate_authority_data"},                                                                                                                                                         // Kubernetes-specific — kubernetes_secret is a code-based masker
		"cloud":      {"aws_access_key", "aws_secret_key", "api_key", "token"},                                                                                                                                                                           // Cloud provider secrets
		"all":        {"base64_secret", "base64_short", "api_key", "password", "certificate", "certificate_authority_data", "email", "token", "ssh_key", "private_key", "secret_key", "aws_access_key", "aws_secret_key", "github_token", "slack_token"}, // All patterns
	}
}

// initBuiltinCodeMaskers returns names of code-based maskers for complex masking scenarios.
// These maskers require structural parsing and can be referenced in PatternGroups.
// Unlike regex patterns in MaskingPatterns, code-based maskers implement custom logic.
//
// Each name must match a Masker registered in pkg/masking/service.go (registerMasker).
// Implementations live in pkg/masking/ — see each masker's Name() method.
func initBuiltinCodeMaskers() []string {
	return []string{
		"kubernetes_secret", // pkg/masking/kubernetes_secret.go
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
