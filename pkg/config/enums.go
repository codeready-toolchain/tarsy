package config

// AgentType determines what the agent does — drives controller selection and agent wrapper.
type AgentType string

const (
	// AgentTypeDefault is a regular investigation agent (iterating controller)
	AgentTypeDefault AgentType = ""
	// AgentTypeSynthesis synthesizes parallel investigation results (single-shot)
	AgentTypeSynthesis AgentType = "synthesis"
	// AgentTypeExecSummary generates an executive summary of the investigation (single-shot)
	AgentTypeExecSummary AgentType = "exec_summary"
	// AgentTypeScoring evaluates session quality (single-shot)
	AgentTypeScoring AgentType = "scoring"
	// AgentTypeAction evaluates findings and executes remediation actions (iterating controller)
	AgentTypeAction AgentType = "action"
)

// IsValid checks if the agent type is valid (empty string is valid — means default).
func (t AgentType) IsValid() bool {
	switch t {
	case AgentTypeDefault, AgentTypeSynthesis, AgentTypeExecSummary, AgentTypeScoring, AgentTypeAction:
		return true
	default:
		return false
	}
}

// LLMBackend determines which SDK path to use for LLM calls.
type LLMBackend string

const (
	// LLMBackendNativeGemini uses the Google SDK directly
	LLMBackendNativeGemini LLMBackend = "google-native"
	// LLMBackendLangChain uses LangChain multi-provider
	LLMBackendLangChain LLMBackend = "langchain"
)

// IsValid checks if the LLM backend is valid (empty string is NOT valid — must be explicit).
func (b LLMBackend) IsValid() bool {
	return b == LLMBackendNativeGemini || b == LLMBackendLangChain
}

// SuccessPolicy defines success criteria for parallel stages
type SuccessPolicy string

const (
	// SuccessPolicyAll requires all agents to succeed
	SuccessPolicyAll SuccessPolicy = "all"
	// SuccessPolicyAny requires at least one agent to succeed (default)
	SuccessPolicyAny SuccessPolicy = "any"
)

// IsValid checks if the success policy is valid
func (p SuccessPolicy) IsValid() bool {
	return p == SuccessPolicyAll || p == SuccessPolicyAny
}

// TransportType defines MCP server transport types
type TransportType string

const (
	// TransportTypeStdio uses subprocess communication via stdin/stdout
	TransportTypeStdio TransportType = "stdio"
	// TransportTypeHTTP uses HTTP/HTTPS JSON-RPC
	TransportTypeHTTP TransportType = "http"
	// TransportTypeSSE uses Server-Sent Events
	TransportTypeSSE TransportType = "sse"
)

// IsValid checks if the transport type is valid
func (t TransportType) IsValid() bool {
	return t == TransportTypeStdio || t == TransportTypeHTTP || t == TransportTypeSSE
}

// LLMProviderType defines supported LLM providers
type LLMProviderType string

const (
	// LLMProviderTypeGoogle is Google Gemini API
	LLMProviderTypeGoogle LLMProviderType = "google"
	// LLMProviderTypeOpenAI is OpenAI API
	LLMProviderTypeOpenAI LLMProviderType = "openai"
	// LLMProviderTypeAnthropic is Anthropic Claude API
	LLMProviderTypeAnthropic LLMProviderType = "anthropic"
	// LLMProviderTypeXAI is xAI Grok API
	LLMProviderTypeXAI LLMProviderType = "xai"
	// LLMProviderTypeVertexAI is Google Vertex AI
	LLMProviderTypeVertexAI LLMProviderType = "vertexai"
)

// IsValid checks if the LLM provider type is valid
func (t LLMProviderType) IsValid() bool {
	switch t {
	case LLMProviderTypeGoogle,
		LLMProviderTypeOpenAI,
		LLMProviderTypeAnthropic,
		LLMProviderTypeXAI,
		LLMProviderTypeVertexAI:
		return true
	default:
		return false
	}
}

// GoogleNativeTool defines Google/Gemini native tools
type GoogleNativeTool string

const (
	// GoogleNativeToolGoogleSearch enables Google Search grounding
	GoogleNativeToolGoogleSearch GoogleNativeTool = "google_search"
	// GoogleNativeToolCodeExecution enables code execution
	GoogleNativeToolCodeExecution GoogleNativeTool = "code_execution"
	// GoogleNativeToolURLContext enables URL context fetching
	GoogleNativeToolURLContext GoogleNativeTool = "url_context"
)

// IsValid checks if the Google native tool is valid
func (t GoogleNativeTool) IsValid() bool {
	return t == GoogleNativeToolGoogleSearch ||
		t == GoogleNativeToolCodeExecution ||
		t == GoogleNativeToolURLContext
}

// IsGoogleNativeToolWireName reports whether name is a Gemini provider-native
// tool wire identifier (google_search, url_context, code_execution). Used to
// avoid routing these through MCP ToolExecutor — they are fulfilled by the API.
func IsGoogleNativeToolWireName(name string) bool {
	t := GoogleNativeTool(name)
	return t == GoogleNativeToolGoogleSearch ||
		t == GoogleNativeToolCodeExecution ||
		t == GoogleNativeToolURLContext
}

// googleNativeToolWireAliases maps model- or skill-emitted names to canonical Gemini native wire names.
// Sub-agents often use StubToolExecutor (no MCP); the API still binds native tools, but the model may
// call a synonym (e.g. from skill text) unless we canonicalize before IsGoogleNativeToolWireName.
var googleNativeToolWireAliases = map[string]GoogleNativeTool{
	"load_context": GoogleNativeToolURLContext,
}

// CanonicalGoogleNativeToolWireName returns the canonical native tool wire name if name is a known
// alias; otherwise returns name unchanged. Only apply when LLMBackendNativeGemini.
func CanonicalGoogleNativeToolWireName(name string) string {
	if t, ok := googleNativeToolWireAliases[name]; ok {
		return string(t)
	}
	return name
}
