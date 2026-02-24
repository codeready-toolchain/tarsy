package config

// AgentType determines what the agent does — drives controller selection and agent wrapper.
type AgentType string

const (
	AgentTypeDefault   AgentType = ""          // Regular investigation agent (iterating controller)
	AgentTypeSynthesis AgentType = "synthesis" // Synthesizes parallel investigation results (single-shot)
	AgentTypeScoring   AgentType = "scoring"   // Evaluates session quality (single-shot)
)

// IsValid checks if the agent type is valid (empty string is valid — means default).
func (t AgentType) IsValid() bool {
	switch t {
	case AgentTypeDefault, AgentTypeSynthesis, AgentTypeScoring:
		return true
	default:
		return false
	}
}

// LLMBackend determines which SDK path to use for LLM calls.
type LLMBackend string

const (
	LLMBackendNativeGemini LLMBackend = "google-native" // Google SDK direct
	LLMBackendLangChain    LLMBackend = "langchain"     // LangChain multi-provider
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
