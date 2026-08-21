package agent

import (
	"cmp"
	"fmt"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// SummarizationExecutionIDSuffix isolates the google-native thought-signature
// cache from the investigation conversation. Timeline and LLMInteraction rows
// keep the unsuffixed ExecutionID.
//
// Multiple summaries in one execution share this key. That is safe because
// each call is a one-shot [system, user] Generate with no assistant history,
// so cached model turns are never replayed into the next dump.
const SummarizationExecutionIDSuffix = ":summarization"

// ResolveSummarizationLLM picks the provider for a tool-result summary.
// Last non-empty llm_provider wins: agent → defaults → this MCP server overlay.
// server is nil for search_past_sessions (defaults only).
// Named-provider layers default omitted llm_backend to langchain and never
// inherit the investigator's backend.
func ResolveSummarizationLLM(execCtx *ExecutionContext, server *config.SummarizationConfig) (ResolvedSummarizationLLM, error) {
	if execCtx == nil || execCtx.Config == nil {
		return ResolvedSummarizationLLM{}, fmt.Errorf("summarization LLM: missing execution config")
	}

	primary := ResolvedSummarizationLLM{
		Provider:     cloneProviderWithoutNativeTools(execCtx.Config.LLMProvider),
		ProviderName: execCtx.Config.LLMProviderName,
		Backend:      execCtx.Config.LLMBackend,
	}

	if execCtx.DefaultSummarization != nil && execCtx.DefaultSummarization.LLMProvider != "" {
		resolved, err := lookupSummarizationProvider(execCtx.LLMProviders, execCtx.DefaultSummarization)
		if err != nil {
			return ResolvedSummarizationLLM{}, err
		}
		primary = resolved
	}

	if server != nil && server.LLMProvider != "" {
		resolved, err := lookupSummarizationProvider(execCtx.LLMProviders, server)
		if err != nil {
			return ResolvedSummarizationLLM{}, err
		}
		primary = resolved
	}

	primary.Backend = cmp.Or(primary.Backend, config.LLMBackendLangChain)
	return primary, nil
}

func lookupSummarizationProvider(reg *config.LLMProviderRegistry, layer *config.SummarizationConfig) (ResolvedSummarizationLLM, error) {
	if reg == nil {
		return ResolvedSummarizationLLM{}, fmt.Errorf("summarization LLM: provider registry is not available")
	}
	cfg, err := reg.Get(layer.LLMProvider)
	if err != nil {
		return ResolvedSummarizationLLM{}, fmt.Errorf("summarization LLM: lookup %q: %w", layer.LLMProvider, err)
	}
	return ResolvedSummarizationLLM{
		Provider:     cloneProviderWithoutNativeTools(cfg),
		ProviderName: layer.LLMProvider,
		Backend:      cmp.Or(layer.LLMBackend, config.LLMBackendLangChain),
	}, nil
}

// cloneProviderWithoutNativeTools shallow-copies p and clears NativeTools so
// the registry map is never mutated.
func cloneProviderWithoutNativeTools(p *config.LLMProviderConfig) *config.LLMProviderConfig {
	if p == nil {
		return nil
	}
	cloned := *p
	cloned.NativeTools = nil
	return &cloned
}
