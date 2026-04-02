package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAgentTypeIsValid(t *testing.T) {
	tests := []struct {
		name      string
		agentType AgentType
		valid     bool
	}{
		{"default (empty)", AgentTypeDefault, true},
		{"synthesis", AgentTypeSynthesis, true},
		{"scoring", AgentTypeScoring, true},
		{"action", AgentTypeAction, true},
		{"invalid", AgentType("invalid"), false},
		{"orchestrator is now invalid", AgentType("orchestrator"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.agentType.IsValid())
		})
	}
}

func TestLLMBackendIsValid(t *testing.T) {
	tests := []struct {
		name    string
		backend LLMBackend
		valid   bool
	}{
		{"google-native", LLMBackendNativeGemini, true},
		{"langchain", LLMBackendLangChain, true},
		{"invalid", LLMBackend("invalid"), false},
		{"empty", LLMBackend(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.backend.IsValid())
		})
	}
}

func TestSuccessPolicyIsValid(t *testing.T) {
	tests := []struct {
		name   string
		policy SuccessPolicy
		valid  bool
	}{
		{"all", SuccessPolicyAll, true},
		{"any", SuccessPolicyAny, true},
		{"invalid", SuccessPolicy("invalid"), false},
		{"empty", SuccessPolicy(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.policy.IsValid())
		})
	}
}

func TestTransportTypeIsValid(t *testing.T) {
	tests := []struct {
		name      string
		transport TransportType
		valid     bool
	}{
		{"stdio", TransportTypeStdio, true},
		{"http", TransportTypeHTTP, true},
		{"sse", TransportTypeSSE, true},
		{"invalid", TransportType("invalid"), false},
		{"empty", TransportType(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.transport.IsValid())
		})
	}
}

func TestLLMProviderTypeIsValid(t *testing.T) {
	tests := []struct {
		name     string
		provider LLMProviderType
		valid    bool
	}{
		{"google", LLMProviderTypeGoogle, true},
		{"openai", LLMProviderTypeOpenAI, true},
		{"anthropic", LLMProviderTypeAnthropic, true},
		{"xai", LLMProviderTypeXAI, true},
		{"vertexai", LLMProviderTypeVertexAI, true},
		{"invalid", LLMProviderType("invalid"), false},
		{"empty", LLMProviderType(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.provider.IsValid())
		})
	}
}

func TestGoogleNativeToolIsValid(t *testing.T) {
	tests := []struct {
		name  string
		tool  GoogleNativeTool
		valid bool
	}{
		{"google_search", GoogleNativeToolGoogleSearch, true},
		{"code_execution", GoogleNativeToolCodeExecution, true},
		{"url_context", GoogleNativeToolURLContext, true},
		{"invalid", GoogleNativeTool("invalid"), false},
		{"empty", GoogleNativeTool(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, tt.tool.IsValid())
		})
	}
}

func TestIsGoogleNativeToolWireName(t *testing.T) {
	assert.True(t, IsGoogleNativeToolWireName("google_search"))
	assert.True(t, IsGoogleNativeToolWireName("url_context"))
	assert.True(t, IsGoogleNativeToolWireName("code_execution"))
	assert.False(t, IsGoogleNativeToolWireName("load_skill"))
	assert.False(t, IsGoogleNativeToolWireName(""))
	assert.False(t, IsGoogleNativeToolWireName("kubernetes.get_pods"))
}
