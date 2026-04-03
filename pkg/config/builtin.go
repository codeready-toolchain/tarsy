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

// BuiltinAgentConfig holds built-in agent metadata (configuration only).
// Agent instantiation/factory pattern is in pkg/agent/factory.go.
type BuiltinAgentConfig struct {
	Description        string
	Type               AgentType
	MCPServers         []string
	CustomInstructions string
	LLMBackend         LLMBackend
	NativeTools        map[GoogleNativeTool]bool
	Skills             *[]string
	RequiredSkills     []string
}

// Built-in agent names. Use these constants instead of string literals
// when referencing built-in agents in resolvers, executors, and tests.
const (
	AgentNameKubernetes  = "KubernetesAgent"
	AgentNameChat        = "ChatAgent"
	AgentNameExecSummary = "ExecSummaryAgent"
	AgentNameSynthesis   = "SynthesisAgent"
	AgentNameScoring     = "ScoringAgent"
)

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
		AgentNameKubernetes: {
			Description: "Kubernetes-specialized agent",
			MCPServers:  []string{"kubernetes-server"},
		},
		AgentNameChat: {
			Description: "Built-in agent for follow-up conversations",
			// No MCPServers — inherits from chain stages via aggregateChainMCPServers.
		},
		AgentNameExecSummary: {
			Description: "Generates executive summary of the investigation",
			Type:        AgentTypeExecSummary,
			// No MCP servers — single-shot, no tools
		},
		AgentNameScoring: {
			Description: "Evaluates session quality via a multi-turn LLM conversation",
			Type:        AgentTypeScoring,
		},
		AgentNameSynthesis: {
			Description: "Synthesizes parallel investigation results",
			Type:        AgentTypeSynthesis,
			CustomInstructions: `You are an Incident Commander synthesizing results from multiple parallel investigations.

Your task:
1. CRITICALLY EVALUATE each investigation's quality - prioritize results with strong evidence and sound reasoning
2. DISREGARD or deprioritize low-quality results that lack supporting evidence or contain logical errors
3. CHECK FOR TOOL DATA vs. ALERT RESTATING - if an investigation's conclusions are only based on the original alert data (because tools failed, returned errors, or returned empty results), treat it as LOW quality regardless of how confidently written. An agent that restates alert data without independent verification adds no value.
4. ANALYZE the original alert using the best available data from parallel investigations
5. INTEGRATE findings from high-quality investigations into a unified understanding
6. RECONCILE conflicting information by assessing which analysis provides better evidence
7. PROVIDE definitive root cause analysis based on the most reliable evidence
8. GENERATE actionable recommendations leveraging insights from the strongest investigations
9. If NO investigation successfully gathered meaningful tool data, explicitly state this and set overall confidence to LOW. Do not produce a high-confidence synthesis from alert-only analyses.

When presenting findings, reference which investigation (agent name/index) produced each key piece of evidence so humans can trace claims back to their source.

Your report must include:
- A clear classification or assessment of the situation using domain-appropriate labels
- An explicit CONFIDENCE level (HIGH, MEDIUM, LOW) with justification
- Specific evidence citations supporting each conclusion

Focus on solving the original alert/issue, not on meta-analyzing agent performance or comparing approaches.`,
		},
		"WebResearcher": {
			Description: "Public web and URL research: GitHub/GitLab repos, documentation sites, vendor runbooks, issue trackers, CVEs, release notes, image or dependency lookups, and external HTTP(S) hostnames exposed via OpenShift Routes. Use when the alert, runbook, or task references a specific HTTPS URL or needs live internet search — Kubernetes and other MCP tools cannot fetch arbitrary public web pages. Prefer dispatching here over guessing from memory when external sources would verify versions, upstream bugs, or repo layout.",
			LLMBackend:  LLMBackendNativeGemini,
			NativeTools: map[GoogleNativeTool]bool{
				GoogleNativeToolGoogleSearch:  true,
				GoogleNativeToolURLContext:    true,
				GoogleNativeToolCodeExecution: false,
			},
			CustomInstructions: `You complete assigned research using native tools only (no cluster access).

- Use the native tool name url_context for URLs (not load_context or other aliases).
- Use url_context when the task names one or more URLs (repos, docs, READMEs, issues, advisories, OpenShift Route hostnames, etc.).
- Use google_search for broad queries (CVEs, known outages, version compatibility, error strings, etc.).

Report findings with sources. Be thorough but concise. Cite only what appears in tool results and grounding — if a tool returns nothing useful, say so explicitly; never invent file paths, manifest contents, or metrics.`,
		},
		"CodeExecutor": {
			Description: "Executes Python code for computation, data analysis, and calculations",
			LLMBackend:  LLMBackendNativeGemini,
			NativeTools: map[GoogleNativeTool]bool{
				GoogleNativeToolGoogleSearch:  false,
				GoogleNativeToolCodeExecution: true,
				GoogleNativeToolURLContext:    false,
			},
			CustomInstructions: `You solve computational tasks by writing and executing Python code.
Show your work. Report results clearly.`,
		},
		"GeneralWorker": {
			Description: "General-purpose agent for analysis, summarization, reasoning, and other tasks",
			CustomInstructions: `You are GeneralWorker, a general-purpose agent.
Complete the assigned task thoroughly and concisely.`,
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
				Enabled:              BoolPtr(true),
				SizeThresholdTokens:  DefaultSizeThresholdTokens,
				SummaryMaxTokenLimit: 1000,
			},
		},
	}
}

func initBuiltinLLMProviders() map[string]LLMProviderConfig {
	// Google native tools factory for Gemini providers.
	// Returns a fresh map each time to avoid shared mutable state across providers.
	geminiNativeTools := func() map[GoogleNativeTool]bool {
		return map[GoogleNativeTool]bool{
			GoogleNativeToolGoogleSearch:  true,
			GoogleNativeToolCodeExecution: false, // Disabled by default
			GoogleNativeToolURLContext:    true,
		}
	}

	// Image model variants don't support url_context (API returns 400).
	geminiImageNativeTools := func() map[GoogleNativeTool]bool {
		return map[GoogleNativeTool]bool{
			GoogleNativeToolGoogleSearch:  true,
			GoogleNativeToolCodeExecution: false,
			GoogleNativeToolURLContext:    false,
		}
	}

	return map[string]LLMProviderConfig{
		// --- Google Gemini ---
		"google-default": {
			Type:                LLMProviderTypeGoogle,
			Model:               "gemini-3-flash-preview",
			APIKeyEnv:           "GOOGLE_API_KEY",
			MaxToolResultTokens: 950000, // Conservative for 1M context
			NativeTools:         geminiNativeTools(),
		},
		"google-image-flash": {
			Type:                LLMProviderTypeGoogle,
			Model:               "gemini-3.1-flash-image-preview",
			APIKeyEnv:           "GOOGLE_API_KEY",
			MaxToolResultTokens: 950000, // Conservative for 1M context
			NativeTools:         geminiImageNativeTools(),
		},
		"gemini-3-flash": {
			Type:                LLMProviderTypeGoogle,
			Model:               "gemini-3-flash-preview",
			APIKeyEnv:           "GOOGLE_API_KEY",
			MaxToolResultTokens: 950000, // Conservative for 1M context
			NativeTools:         geminiNativeTools(),
		},
		"gemini-3.1-pro": {
			Type:                LLMProviderTypeGoogle,
			Model:               "gemini-3.1-pro-preview",
			APIKeyEnv:           "GOOGLE_API_KEY",
			MaxToolResultTokens: 950000, // Conservative for 1M context
			NativeTools:         geminiNativeTools(),
		},
		"gemini-2.5-flash": {
			Type:                LLMProviderTypeGoogle,
			Model:               "gemini-2.5-flash",
			APIKeyEnv:           "GOOGLE_API_KEY",
			MaxToolResultTokens: 950000, // Conservative for 1M context
			NativeTools:         geminiNativeTools(),
		},
		"gemini-2.5-pro": {
			Type:                LLMProviderTypeGoogle,
			Model:               "gemini-2.5-pro",
			APIKeyEnv:           "GOOGLE_API_KEY",
			MaxToolResultTokens: 950000, // Conservative for 1M context
			NativeTools:         geminiNativeTools(),
		},

		// --- OpenAI ---
		"openai-default": {
			Type:                LLMProviderTypeOpenAI,
			Model:               "gpt-5.2",
			APIKeyEnv:           "OPENAI_API_KEY",
			MaxToolResultTokens: 380000, // Conservative for 400K context
		},

		// --- Anthropic ---
		"anthropic-default": {
			Type:                LLMProviderTypeAnthropic,
			Model:               "claude-sonnet-4-6-20260217",
			APIKeyEnv:           "ANTHROPIC_API_KEY",
			MaxToolResultTokens: 900000, // Conservative for 1M context (beta)
		},

		// --- xAI ---
		"xai-default": {
			Type:                LLMProviderTypeXAI,
			Model:               "grok-4-1-fast-reasoning",
			APIKeyEnv:           "XAI_API_KEY",
			MaxToolResultTokens: 1500000, // Conservative for 2M context
		},

		// --- Vertex AI ---
		"vertexai-default": {
			Type:                LLMProviderTypeVertexAI,
			Model:               "claude-sonnet-4-6",     // Claude Sonnet 4.6 on Vertex AI
			ProjectEnv:          "GOOGLE_CLOUD_PROJECT",  // Standard GCP project ID env var
			LocationEnv:         "GOOGLE_CLOUD_LOCATION", // Standard GCP location env var
			MaxToolResultTokens: 900000,                  // Conservative for 1M context (beta)
		},
	}
}

func initBuiltinChains() map[string]ChainConfig {
	return map[string]ChainConfig{
		"kubernetes": {
			AlertTypes:  []string{"kubernetes"},
			Description: "Single-stage Kubernetes analysis",
			Stages: []StageConfig{
				{
					Name: "analysis",
					Agents: []StageAgentConfig{
						{Name: AgentNameKubernetes},
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
			Pattern:     `(?i)(?:(?:token|bearer|jwt)["\']?\s*[:=]\s*["\']?([A-Za-z0-9_\-\.]{20,})["\']?|--token(?:=|\s+)(?:"[^"\n]+"|'[^'\n]+'|[^\s"']+))`,
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
		"basic":      {"api_key", "password"},                                                                                                                                                                                                                                 // Most common secrets
		"secrets":    {"api_key", "password", "token", "private_key", "secret_key"},                                                                                                                                                                                           // Basic + tokens
		"security":   {"api_key", "password", "token", "certificate", "certificate_authority_data", "email", "ssh_key"},                                                                                                                                                       // Full security focus
		"kubernetes": {"kubernetes_secret", "api_key", "password", "certificate_authority_data"},                                                                                                                                                                              // Kubernetes-specific — kubernetes_secret is a code-based masker
		"cloud":      {"aws_access_key", "aws_secret_key", "api_key", "token"},                                                                                                                                                                                                // Cloud provider secrets
		"all":        {"kubernetes_secret", "base64_secret", "base64_short", "api_key", "password", "certificate", "certificate_authority_data", "email", "token", "ssh_key", "private_key", "secret_key", "aws_access_key", "aws_secret_key", "github_token", "slack_token"}, // All patterns (including code-based maskers)
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

// No default runbook — the system prompt layers (generalInstructions + analysisTask)
// already provide investigation methodology. The runbook slot is reserved for
// organization-specific content configured via defaults.runbook or per-alert URLs.
const defaultRunbookContent = ""
