package config

// IterationStrategy defines available agent iteration strategies
type IterationStrategy string

const (
	// IterationStrategyReact uses standard ReAct pattern with tool calls
	IterationStrategyReact IterationStrategy = "react"
	// IterationStrategyReactStage is ReAct optimized for multi-stage chains
	IterationStrategyReactStage IterationStrategy = "react-stage"
	// IterationStrategyReactFinalAnalysis is ReAct for final analysis stage
	IterationStrategyReactFinalAnalysis IterationStrategy = "react-final-analysis"
	// IterationStrategyNativeThinking uses LLM native thinking/reasoning
	IterationStrategyNativeThinking IterationStrategy = "native-thinking"
	// IterationStrategySynthesis synthesizes parallel investigation results
	IterationStrategySynthesis IterationStrategy = "synthesis"
	// IterationStrategySynthesisNativeThinking is synthesis with native thinking
	IterationStrategySynthesisNativeThinking IterationStrategy = "synthesis-native-thinking"
)

// IsValid checks if the iteration strategy is valid
func (s IterationStrategy) IsValid() bool {
	switch s {
	case IterationStrategyReact,
		IterationStrategyReactStage,
		IterationStrategyReactFinalAnalysis,
		IterationStrategyNativeThinking,
		IterationStrategySynthesis,
		IterationStrategySynthesisNativeThinking:
		return true
	default:
		return false
	}
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
