package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/cost"
)

func TestSanitizeTransport(t *testing.T) {
	t.Run("stdio with env and args redacts secrets", func(t *testing.T) {
		got := sanitizeTransport(config.TransportConfig{
			Type:    config.TransportTypeStdio,
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-kubernetes", "--token", "secret-value"},
			Env: map[string]string{
				"KUBECONFIG": "/path/to/kubeconfig",
				"API_TOKEN":  "super-secret",
			},
		})

		assert.Equal(t, "stdio", got.Type)
		assert.Equal(t, "npx", got.Command)
		assert.Equal(t, []string{"***"}, got.Args)
		assert.Equal(t, []string{"API_TOKEN", "KUBECONFIG"}, got.EnvKeys)
		assert.False(t, got.BearerTokenSet)
		assert.Empty(t, got.URL)

		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "super-secret")
		assert.NotContains(t, string(raw), "secret-value")
		assert.NotContains(t, string(raw), "/path/to/kubeconfig")
	})

	t.Run("http transport sets bearer_token_set and sanitizes url", func(t *testing.T) {
		verify := true
		got := sanitizeTransport(config.TransportConfig{
			Type:        config.TransportTypeHTTP,
			URL:         "https://user:live-secret@mcp.example.com/v1?token=live-query-secret#frag",
			BearerToken: "live-bearer-token",
			VerifySSL:   &verify,
			Timeout:     30,
			CustomHeaders: map[string]string{
				"X-Session-ID": "{{.SESSION_ID}}",
			},
			SessionCleanupURL: "https://user:live-secret@mcp.example.com/sessions/{{.SESSION_ID}}?token=live-query-secret",
		})

		assert.Equal(t, "http", got.Type)
		assert.Equal(t, "https://mcp.example.com/v1", got.URL)
		assert.True(t, got.BearerTokenSet)
		assert.Equal(t, &verify, got.VerifySSL)
		assert.Equal(t, 30, got.Timeout)
		assert.Empty(t, got.Command)
		assert.Nil(t, got.Args)
		assert.Equal(t, []string{"X-Session-ID"}, got.CustomHeaderKeys)
		assert.Equal(t, "https://mcp.example.com/sessions/{{.SESSION_ID}}", got.SessionCleanupURL)

		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "live-secret")
		assert.NotContains(t, string(raw), "live-query-secret")
		assert.NotContains(t, string(raw), "live-bearer-token")
		assert.Contains(t, string(raw), `"bearer_token_set":true`)
		assert.Contains(t, string(raw), `"custom_header_keys"`)
		assert.Contains(t, string(raw), `"session_cleanup_url"`)
		assert.Contains(t, string(raw), `/sessions/{{.SESSION_ID}}`)
		// Custom header *values* must stay out of the sanitized view.
		assert.NotContains(t, string(raw), `"X-Session-ID":"{{.SESSION_ID}}"`)
	})

	t.Run("empty args and url omitted", func(t *testing.T) {
		got := sanitizeTransport(config.TransportConfig{
			Type:    config.TransportTypeStdio,
			Command: "npx",
		})
		assert.Nil(t, got.Args)
		assert.Empty(t, got.URL)
		assert.Nil(t, got.EnvKeys)
		assert.False(t, got.BearerTokenSet)
	})
}

func TestDurationString(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "zero", in: 0, want: "0s"},
		{name: "seconds", in: 5 * time.Second, want: "5s"},
		{name: "minutes strips trailing zero seconds", in: 40 * time.Minute, want: "40m"},
		{name: "hours strips trailing zero units", in: 168 * time.Hour, want: "168h"},
		{name: "compound preserves non-zero units", in: time.Hour + 30*time.Minute, want: "1h30m"},
		{name: "compound with seconds", in: time.Hour + 30*time.Minute + 5*time.Second, want: "1h30m5s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, durationString(tt.in))
		})
	}
}

func TestLooksSecretBearing(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "normal npx", in: "npx", want: false},
		{name: "normal binary path", in: "/usr/local/bin/mcp-server", want: false},
		{name: "short path not token-like", in: "./bin/mcp", want: false},
		{name: "sk substring in ordinary name", in: "risk-management-tool", want: false},
		{name: "long path with separators", in: "/usr/local/share/mcp/servers/kubernetes-tools/bin/run", want: false},
		{name: "ghp prefix", in: "ghp_FAKE_NOT_REAL_GITHUB_TOKEN_XXXXXXXXXXXX", want: true},
		{name: "gho prefix", in: "gho_FAKE_NOT_REAL_GITHUB_OAUTH_TOKEN_XXXXXX", want: true},
		{name: "github_pat prefix", in: "github_pat_FAKE_NOT_REAL_FINE_GRAINED_XXXX", want: true},
		{name: "xoxb prefix", in: "xoxb-FAKE-NOT-REAL-SLACK-BOT-TOKEN-XXXXXXXXXX", want: true},
		{name: "xoxp prefix", in: "xoxp-FAKE-NOT-REAL-SLACK-USER-TOKEN-XXXXXXX", want: true},
		{name: "sk prefix", in: "sk-FAKE-NOT-REAL-API-KEY-XXXXXXXXXXXX", want: true},
		{name: "sk prefix after separator", in: "TOKEN=sk-FAKE-NOT-REAL-API-KEY-XXXXXXXXXXXX", want: true},
		{name: "AKIA prefix", in: "AKIAFAKENOTREALEXAMPLE00", want: true},
		{name: "bearer prefix", in: "Bearer FAKE.NOT.REAL.JWT.HEADER", want: true},
		{name: "jwt like", in: "eyJFAKE.NOT.REAL.JWT.PAYLOAD.SIGNATURE", want: true},
		{name: "long token substring", in: "bin-ABCDEFGHIJKLMNOPQRSTUVWXYZ012345", want: true},
		{name: "empty", in: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, looksSecretBearing(tt.in))
		})
	}
}

func TestSystemConfigHandler(t *testing.T) {
	t.Run("nil registries yield empty objects", func(t *testing.T) {
		s := &Server{cfg: &config.Config{}}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/config", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.systemConfigHandler(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp SystemConfigResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotNil(t, resp.FallbackLists)
		assert.Empty(t, resp.FallbackLists)
		assert.NotNil(t, resp.Agents)
		assert.Empty(t, resp.Agents)
		assert.NotNil(t, resp.Chains)
		assert.Empty(t, resp.Chains)
		assert.NotNil(t, resp.MCPServers)
		assert.Empty(t, resp.MCPServers)
		assert.NotNil(t, resp.LLMProviders)
		assert.Empty(t, resp.LLMProviders)
		assert.NotNil(t, resp.Skills)
		assert.Empty(t, resp.Skills)
		assert.NotNil(t, resp.System.AllowedWSOrigins)
	})

	t.Run("sanitizes mcp transport and includes instructions", func(t *testing.T) {
		s := &Server{
			cfg: &config.Config{
				Queue: &config.QueueConfig{
					WorkerCount:    5,
					PollInterval:   5 * time.Second,
					SessionTimeout: 40 * time.Minute,
				},
				AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
					"KubernetesAgent": {
						Description:        "K8s agent",
						CustomInstructions: "Investigate pods carefully",
						MCPServers:         []string{"kubernetes-server"},
					},
				}),
				MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
					"kubernetes-server": {
						Transport: config.TransportConfig{
							Type:        config.TransportTypeStdio,
							Command:     "npx",
							Args:        []string{"-y", "secret-arg"},
							Env:         map[string]string{"KUBECONFIG": "/secret/path"},
							BearerToken: "should-not-appear",
						},
						Instructions: "Use kubectl carefully",
						Summarization: &config.SummarizationConfig{
							SizeThresholdTokens:  5000,
							SummaryMaxTokenLimit: 1000,
							LLMProvider:          "google-default",
							LLMBackend:           config.LLMBackendLangChain,
						},
					},
					"alpha-server": {
						Transport: config.TransportConfig{
							Type:    config.TransportTypeStdio,
							Command: "npx",
						},
					},
				}),
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"google-default": {
						Type:      config.LLMProviderTypeGoogle,
						Model:     "gemini-2.5-pro",
						APIKeyEnv: "GOOGLE_API_KEY",
					},
				}),
				SkillRegistry: config.NewSkillRegistry(map[string]*config.SkillConfig{
					"example-skill": {
						Name:        "example-skill",
						Description: "An example",
						Body:        "full body should not be in snapshot",
					},
				}),
				GitHub: &config.GitHubConfig{TokenEnv: "GITHUB_TOKEN"},
				Slack: &config.SlackConfig{
					Enabled:  true,
					TokenEnv: "SLACK_BOT_TOKEN",
					Channel:  "C123",
				},
				Runbooks: &config.RunbookConfig{
					RepoURL:  "https://github.com/example/runbooks",
					CacheTTL: time.Minute,
				},
				Retention: &config.RetentionConfig{
					SessionRetentionDays: 30,
					EventTTL:             168 * time.Hour,
					CleanupInterval:      time.Hour,
				},
				DashboardURL: "https://tarsy.example.com",
			},
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/config", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.systemConfigHandler(c)
		require.NoError(t, err)

		body := rec.Body.String()
		assert.NotContains(t, body, "should-not-appear")
		assert.NotContains(t, body, "secret-arg")
		assert.NotContains(t, body, "/secret/path")
		assert.NotContains(t, body, "full body should not be in snapshot")
		assert.Contains(t, body, "Investigate pods carefully")
		assert.Contains(t, body, "Use kubectl carefully")
		assert.Contains(t, body, "GOOGLE_API_KEY")
		assert.Contains(t, body, "GITHUB_TOKEN")

		var resp SystemConfigResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

		require.NotNil(t, resp.Queue)
		assert.Equal(t, "5s", resp.Queue.PollInterval)
		assert.Equal(t, "40m", resp.Queue.SessionTimeout)

		require.NotNil(t, resp.System.Retention)
		assert.Equal(t, "168h", resp.System.Retention.EventTTL)
		assert.Equal(t, "1h", resp.System.Retention.CleanupInterval)

		// Sorted map keys: alpha-server before kubernetes-server
		assert.Equal(t, []string{"alpha-server", "kubernetes-server"}, sortedKeys(resp.MCPServers))

		k8s := resp.MCPServers["kubernetes-server"]
		assert.Equal(t, "npx", k8s.Transport.Command)
		assert.Equal(t, []string{"***"}, k8s.Transport.Args)
		assert.Equal(t, []string{"KUBECONFIG"}, k8s.Transport.EnvKeys)
		assert.True(t, k8s.Transport.BearerTokenSet)
		assert.Equal(t, "Use kubectl carefully", k8s.Instructions)
		require.NotNil(t, k8s.Summarization)
		assert.Equal(t, 5000, k8s.Summarization.SizeThresholdTokens)
		assert.Equal(t, 1000, k8s.Summarization.SummaryMaxTokenLimit)
		assert.Equal(t, "google-default", k8s.Summarization.LLMProvider)
		assert.Equal(t, "langchain", k8s.Summarization.LLMBackend)
		mcpSumRaw, err := json.Marshal(k8s.Summarization)
		require.NoError(t, err)
		assert.NotContains(t, string(mcpSumRaw), `"fallback_list"`)

		agent := resp.Agents["KubernetesAgent"]
		assert.Equal(t, "Investigate pods carefully", agent.CustomInstructions)

		skill := resp.Skills["example-skill"]
		assert.Equal(t, "example-skill", skill.Name)
		assert.Equal(t, "An example", skill.Description)
	})

	t.Run("secret-looking command is redacted", func(t *testing.T) {
		s := &Server{
			cfg: &config.Config{
				MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
					"bad": {
						Transport: config.TransportConfig{
							Type:    config.TransportTypeStdio,
							Command: "ghp_FAKE_NOT_REAL_GITHUB_TOKEN_XXXXXXXXXXXX",
						},
					},
				}),
			},
		}

		resp := buildSystemConfigResponse(s.cfg, nil)
		assert.Equal(t, "***", resp.MCPServers["bad"].Transport.Command)
	})

	t.Run("nil config yields empty maps", func(t *testing.T) {
		s := &Server{cfg: nil}
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/config", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.systemConfigHandler(c)
		require.NoError(t, err)

		var resp SystemConfigResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Empty(t, resp.FallbackLists)
		assert.Empty(t, resp.Agents)
		assert.Empty(t, resp.Chains)
		assert.Empty(t, resp.MCPServers)
	})

	t.Run("includes cost_estimation from book status", func(t *testing.T) {
		book, err := cost.NewBook(&cost.Config{
			Enabled: true,
			ModelRates: map[string]cost.ModelRateOverride{
				"gemini-3.1-pro-preview": {InputPerMillion: 2.0, OutputPerMillion: 12.0},
			},
		})
		require.NoError(t, err)

		s := &Server{
			cfg: &config.Config{
				CostEstimation: &config.CostEstimationConfig{Enabled: true},
			},
			costBook: book,
		}
		resp := buildSystemConfigResponse(s.cfg, s.costBook)
		require.NotNil(t, resp.System.CostEstimation)
		assert.True(t, resp.System.CostEstimation.Enabled)
		require.Contains(t, resp.System.CostEstimation.ModelRates, "gemini-3.1-pro-preview")
		assert.Equal(t, 2.0, resp.System.CostEstimation.ModelRates["gemini-3.1-pro-preview"].InputPerMillion)
		assert.Equal(t, "snapshot", resp.System.CostEstimation.Catalog.Source)
		assert.Greater(t, resp.System.CostEstimation.Catalog.EntryCount, 0)
	})

	t.Run("includes promotions from book status", func(t *testing.T) {
		start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		book, err := cost.NewBook(&cost.Config{
			Enabled: true,
			Promotions: []cost.Promotion{{
				ID:               "gemini-3.7-flash-intro",
				Model:            "gemini-3.7-flash",
				Start:            &start,
				End:              end,
				InputPerMillion:  0.75,
				OutputPerMillion: 3.75,
			}},
		})
		require.NoError(t, err)
		book.SetNowForTest(func() time.Time {
			return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		})

		resp := buildSystemConfigResponse(&config.Config{
			CostEstimation: &config.CostEstimationConfig{Enabled: true},
		}, book)
		require.NotNil(t, resp.System.CostEstimation)
		require.Len(t, resp.System.CostEstimation.Promotions, 1)
		p := resp.System.CostEstimation.Promotions[0]
		assert.Equal(t, "gemini-3.7-flash-intro", p.ID)
		assert.Equal(t, "gemini-3.7-flash", p.Model)
		assert.Equal(t, 0.75, p.InputPerMillion)
		assert.Equal(t, "active", p.Status)
		require.NotNil(t, p.Start)
		assert.Equal(t, start.UTC().Format(time.RFC3339), *p.Start)
		assert.Equal(t, end.UTC().Format(time.RFC3339), p.End)
	})

	t.Run("cost_estimation falls back to config when book nil", func(t *testing.T) {
		resp := buildSystemConfigResponse(&config.Config{
			CostEstimation: &config.CostEstimationConfig{
				Enabled: false,
				ModelRates: map[string]config.ModelRateConfig{
					"gpt-4o": {InputPerMillion: 2.5, OutputPerMillion: 10.0},
				},
				Promotions: []config.PromotionConfig{{
					Model:            "promo-model",
					Start:            "2026-08-01",
					End:              "2026-10-01",
					InputPerMillion:  0.5,
					OutputPerMillion: 1.5,
				}},
			},
		}, nil)
		require.NotNil(t, resp.System.CostEstimation)
		assert.False(t, resp.System.CostEstimation.Enabled)
		assert.Equal(t, 2.5, resp.System.CostEstimation.ModelRates["gpt-4o"].InputPerMillion)
		assert.Equal(t, "none", resp.System.CostEstimation.Catalog.Source)
		require.Len(t, resp.System.CostEstimation.Promotions, 1)
		assert.Equal(t, "promo-model", resp.System.CostEstimation.Promotions[0].Model)
		assert.NotEmpty(t, resp.System.CostEstimation.Promotions[0].Status)
	})

	t.Run("includes prompt_caching enabled by default", func(t *testing.T) {
		resp := buildSystemConfigResponse(&config.Config{}, nil)
		require.NotNil(t, resp.System.PromptCaching)
		assert.True(t, resp.System.PromptCaching.Enabled)
	})

	t.Run("includes prompt_caching disabled from config", func(t *testing.T) {
		resp := buildSystemConfigResponse(&config.Config{
			PromptCaching: &config.PromptCachingConfig{Enabled: false},
		}, nil)
		require.NotNil(t, resp.System.PromptCaching)
		assert.False(t, resp.System.PromptCaching.Enabled)
	})

	t.Run("includes chains defaults memory and orchestrator duration strings", func(t *testing.T) {
		maxIter := 10
		maxConcurrent := 3
		agentTimeout := 2 * time.Minute
		emptySkills := []string{}

		s := &Server{
			cfg: &config.Config{
				Defaults: &config.Defaults{
					LLMProvider:   "google-default",
					Compose:       &config.JobPairing{LLMProvider: "google-default"},
					MaxIterations: &maxIter,
					LLMBackend:    config.LLMBackendNativeGemini,
					Summarization: &config.SummarizationConfig{
						LLMProvider: "google-default",
						LLMBackend:  config.LLMBackendLangChain,
					},
					Memory: &config.MemoryConfig{
						Enabled:   true,
						MaxInject: 5,
						Embedding: config.EmbeddingConfig{
							Provider:   config.EmbeddingProviderGoogle,
							Model:      "gemini-embedding-2-preview",
							APIKeyEnv:  "GOOGLE_API_KEY",
							Dimensions: 768,
						},
					},
					Orchestrator: &config.OrchestratorConfig{
						MaxConcurrentAgents: &maxConcurrent,
						AgentTimeout:        &agentTimeout,
					},
				},
				AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
					"Worker": {
						CustomInstructions: "work",
						Skills:             &emptySkills,
						RequiredSkills:     []string{"req-skill"},
					},
					"OrchestratorAgent": {
						CustomInstructions: "orchestrate",
						Orchestrator: &config.OrchestratorConfig{
							AgentTimeout: &agentTimeout,
						},
					},
				}),
				ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
					"zeta-chain": {
						AlertTypes:  []string{"ZetaAlert"},
						Description: "Z chain",
						Stages: []config.StageConfig{
							{Name: "only", Agents: []config.StageAgentConfig{{Name: "Worker"}}},
						},
					},
					"alpha-chain": {
						AlertTypes:  []string{"AlphaAlert"},
						Description: "A chain",
						LLMProvider: "google-default",
						Compose:     &config.JobPairing{LLMProvider: "google-default"},
						Stages: []config.StageConfig{
							{
								Name: "investigate",
								Agents: []config.StageAgentConfig{
									{Name: "Worker", LLMProvider: "google-default"},
								},
							},
						},
						Chat: &config.ChatConfig{Agent: "Worker"},
					},
				}),
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"google-default": {
						Type:      config.LLMProviderTypeGoogle,
						Model:     "gemini-2.5-pro",
						APIKeyEnv: "GOOGLE_API_KEY",
						BaseURL:   "https://generativelanguage.googleapis.com",
					},
				}),
			},
		}

		resp := buildSystemConfigResponse(s.cfg, nil)

		require.NotNil(t, resp.Defaults)
		assert.Equal(t, "google-default", resp.Defaults.LLMProvider)
		require.NotNil(t, resp.Defaults.Compose)
		assert.Equal(t, "google-default", resp.Defaults.Compose.LLMProvider)
		require.NotNil(t, resp.Defaults.Summarization)
		assert.Equal(t, "google-default", resp.Defaults.Summarization.LLMProvider)
		assert.Equal(t, "langchain", resp.Defaults.Summarization.LLMBackend)
		rawDefaults, err := json.Marshal(resp.Defaults)
		require.NoError(t, err)
		var defaultsJSON map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rawDefaults, &defaultsJSON))
		sumRaw, ok := defaultsJSON["summarization"]
		require.True(t, ok)
		var sumJSON map[string]any
		require.NoError(t, json.Unmarshal(sumRaw, &sumJSON))
		assert.Equal(t, map[string]any{
			"llm_provider": "google-default",
			"llm_backend":  "langchain",
		}, sumJSON)
		require.NotNil(t, resp.Defaults.Memory)
		assert.Equal(t, "GOOGLE_API_KEY", resp.Defaults.Memory.Embedding.APIKeyEnv)
		require.NotNil(t, resp.Defaults.Orchestrator)
		require.NotNil(t, resp.Defaults.Orchestrator.AgentTimeout)
		assert.Equal(t, "2m", *resp.Defaults.Orchestrator.AgentTimeout)

		assert.Equal(t, []string{"alpha-chain", "zeta-chain"}, sortedKeys(resp.Chains))
		alpha := resp.Chains["alpha-chain"]
		assert.Equal(t, []string{"AlphaAlert"}, alpha.AlertTypes)
		assert.Equal(t, "google-default", alpha.LLMProvider)
		require.NotNil(t, alpha.Compose)
		assert.Equal(t, "google-default", alpha.Compose.LLMProvider)
		require.Len(t, alpha.Stages, 1)
		assert.Equal(t, "investigate", alpha.Stages[0].Name)
		require.NotNil(t, alpha.Chat)
		assert.True(t, alpha.Chat.Enabled)

		worker := resp.Agents["Worker"]
		require.NotNil(t, worker.Skills)
		assert.Empty(t, *worker.Skills)
		assert.Equal(t, []string{"req-skill"}, worker.RequiredSkills)

		orch := resp.Agents["OrchestratorAgent"]
		require.NotNil(t, orch.Orchestrator)
		require.NotNil(t, orch.Orchestrator.AgentTimeout)
		assert.Equal(t, "2m", *orch.Orchestrator.AgentTimeout)

		provider := resp.LLMProviders["google-default"]
		assert.Equal(t, "https://generativelanguage.googleapis.com", provider.BaseURL)
		assert.Equal(t, "GOOGLE_API_KEY", provider.APIKeyEnv)

		// Agents must not expose llm_provider (selection lives on defaults/chains).
		raw, err := json.Marshal(resp.Agents)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"llm_provider"`)
		assert.NotContains(t, string(raw), `"fallback_list"`)
	})

	t.Run("fail-closed transport JSON omits secret field names", func(t *testing.T) {
		got := sanitizeTransport(config.TransportConfig{
			Type:        config.TransportTypeHTTP,
			URL:         "https://example.com",
			BearerToken: "secret-token",
			Env:         map[string]string{"TOKEN": "value"},
			Args:        []string{"--secret"},
		})
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		body := string(raw)

		assert.NotContains(t, body, `"bearer_token"`)
		assert.NotContains(t, body, `"env"`)
		assert.NotContains(t, body, "secret-token")
		assert.NotContains(t, body, "value")
		assert.NotContains(t, body, "--secret")
		assert.Contains(t, body, `"bearer_token_set":true`)
		assert.Contains(t, body, `"env_keys"`)
		assert.Equal(t, "https://example.com", got.URL)
	})

	t.Run("llm provider base_url strips credentials and query", func(t *testing.T) {
		resp := buildSystemConfigResponse(&config.Config{
			LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
				"custom": {
					Type:      config.LLMProviderTypeOpenAI,
					Model:     "gpt-test",
					APIKeyEnv: "OPENAI_API_KEY",
					BaseURL:   "https://api:E2E_SENTINEL_BASE_URL@llm.example.com/v1?api_key=E2E_SENTINEL_QUERY",
				},
			}),
		}, nil)
		provider := resp.LLMProviders["custom"]
		assert.Equal(t, "https://llm.example.com/v1", provider.BaseURL)
		raw, err := json.Marshal(provider)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "E2E_SENTINEL_BASE_URL")
		assert.NotContains(t, string(raw), "E2E_SENTINEL_QUERY")
	})

	t.Run("omitted fallback backend is langchain in the config view", func(t *testing.T) {
		resp := buildSystemConfigResponse(&config.Config{
			Defaults: &config.Defaults{
				LLMBackend: config.LLMBackendNativeGemini,
				FallbackProviders: []config.FallbackProviderEntry{
					{Provider: "claude-fb"},
					{Provider: "gemini-fb", Backend: config.LLMBackendNativeGemini},
				},
			},
		}, nil)
		require.NotNil(t, resp.Defaults)
		require.Len(t, resp.Defaults.FallbackProviders, 2)
		assert.Equal(t, FallbackProviderView{Provider: "claude-fb", Backend: "langchain"},
			resp.Defaults.FallbackProviders[0])
		assert.Equal(t, FallbackProviderView{Provider: "gemini-fb", Backend: "google-native"},
			resp.Defaults.FallbackProviders[1])
	})
}

func TestBuildSystemConfigResponse_NamedFallbackLists(t *testing.T) {
	t.Run("emits catalog selectors and pairing as written", func(t *testing.T) {
		resp := buildSystemConfigResponse(&config.Config{
			FallbackLists: map[string][]config.FallbackProviderEntry{
				"premium": {
					{Provider: "claude-opus"},
					{Provider: "gemini-pro", Backend: config.LLMBackendNativeGemini},
				},
				"empty": {},
				"mid": {
					{Provider: "claude-sonnet"},
				},
			},
			Defaults: &config.Defaults{
				LLMProvider:  "claude-opus",
				FallbackList: "premium",
				Compose: &config.JobPairing{
					LLMProvider:  "claude-sonnet",
					LLMBackend:   config.LLMBackendLangChain,
					FallbackList: "mid",
				},
				ExecutiveSummary: &config.JobPairing{
					LLMProvider:  "claude-sonnet",
					LLMBackend:   config.LLMBackendNativeGemini,
					FallbackList: "mid",
				},
				Scoring: &config.ScoringConfig{
					Enabled:      true,
					LLMProvider:  "claude-sonnet",
					FallbackList: "mid",
				},
				Summarization: &config.SummarizationConfig{
					LLMProvider:  "google-default",
					LLMBackend:   config.LLMBackendNativeGemini,
					FallbackList: "empty",
				},
				Agents: map[string]config.NamedAgentPairing{
					"WebResearcher": {
						LLMProvider:  "google-default",
						LLMBackend:   config.LLMBackendNativeGemini,
						FallbackList: "empty",
					},
					"ChatAgent": {
						FallbackList: "mid",
					},
				},
			},
			AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
				"WebResearcher": {
					CustomInstructions: "research",
					LLMBackend:         config.LLMBackendNativeGemini,
				},
			}),
			ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
				"main": {
					AlertTypes:   []string{"TestAlert"},
					LLMProvider:  "claude-opus",
					FallbackList: "premium",
					Compose: &config.JobPairing{
						LLMProvider:  "claude-sonnet",
						LLMBackend:   config.LLMBackendLangChain,
						FallbackList: "mid",
					},
					ExecutiveSummary: &config.JobPairing{
						LLMProvider:  "claude-sonnet",
						FallbackList: "mid",
					},
					FallbackProviders: []config.FallbackProviderEntry{
						{Provider: "legacy-inline"},
					},
					SubAgents: config.SubAgentRefs{
						{Name: "WebResearcher", FallbackList: "empty"},
					},
					Chat: &config.ChatConfig{
						FallbackList: "mid",
						SubAgents: config.SubAgentRefs{
							{Name: "WebResearcher", LLMProvider: "google-default"},
						},
					},
					Scoring: &config.ScoringConfig{
						Enabled:      true,
						FallbackList: "mid",
					},
					Stages: []config.StageConfig{
						{
							Name:         "investigate",
							FallbackList: "premium",
							Agents: []config.StageAgentConfig{
								{
									Name:         "Worker",
									LLMProvider:  "claude-opus",
									FallbackList: "premium",
									SubAgents: config.SubAgentRefs{
										{Name: "WebResearcher", FallbackList: "empty"},
									},
								},
							},
							Synthesis: &config.SynthesisConfig{
								LLMProvider:  "claude-sonnet",
								FallbackList: "mid",
							},
						},
					},
				},
			}),
			MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
				"k8s": {
					Transport: config.TransportConfig{Type: config.TransportTypeStdio, Command: "npx"},
					Summarization: &config.SummarizationConfig{
						LLMProvider:  "google-default",
						FallbackList: "must-not-appear",
					},
				},
			}),
		}, nil)

		assert.Equal(t, []string{"empty", "mid", "premium"}, sortedKeys(resp.FallbackLists))
		require.Len(t, resp.FallbackLists["premium"], 2)
		assert.Equal(t, CatalogFallbackEntryView{LLMProvider: "claude-opus", LLMBackend: "langchain"},
			resp.FallbackLists["premium"][0])
		assert.Equal(t, CatalogFallbackEntryView{LLMProvider: "gemini-pro", LLMBackend: "google-native"},
			resp.FallbackLists["premium"][1])
		require.NotNil(t, resp.FallbackLists["empty"])
		assert.Empty(t, resp.FallbackLists["empty"])
		require.Len(t, resp.FallbackLists["mid"], 1)
		assert.Equal(t, "claude-sonnet", resp.FallbackLists["mid"][0].LLMProvider)

		require.NotNil(t, resp.Defaults)
		assert.Equal(t, "premium", resp.Defaults.FallbackList)
		require.NotNil(t, resp.Defaults.Compose)
		assert.Equal(t, "mid", resp.Defaults.Compose.FallbackList)
		assert.Equal(t, "langchain", resp.Defaults.Compose.LLMBackend)
		composeRaw, err := json.Marshal(resp.Defaults.Compose)
		require.NoError(t, err)
		assert.JSONEq(t, `{"llm_provider":"claude-sonnet","llm_backend":"langchain","fallback_list":"mid"}`, string(composeRaw))
		require.NotNil(t, resp.Defaults.ExecutiveSummary)
		assert.Equal(t, "claude-sonnet", resp.Defaults.ExecutiveSummary.LLMProvider)
		assert.Equal(t, "google-native", resp.Defaults.ExecutiveSummary.LLMBackend)
		assert.Equal(t, "mid", resp.Defaults.ExecutiveSummary.FallbackList)
		execRaw, err := json.Marshal(resp.Defaults.ExecutiveSummary)
		require.NoError(t, err)
		assert.JSONEq(t, `{"llm_provider":"claude-sonnet","llm_backend":"google-native","fallback_list":"mid"}`, string(execRaw))
		require.NotNil(t, resp.Defaults.Scoring)
		assert.Equal(t, "mid", resp.Defaults.Scoring.FallbackList)
		require.NotNil(t, resp.Defaults.Summarization)
		assert.Equal(t, "empty", resp.Defaults.Summarization.FallbackList)
		assert.Equal(t, []string{"ChatAgent", "WebResearcher"}, sortedKeys(resp.Defaults.Agents))
		assert.Equal(t, NamedAgentPairingView{
			LLMProvider:  "google-default",
			LLMBackend:   "google-native",
			FallbackList: "empty",
		}, resp.Defaults.Agents["WebResearcher"])
		assert.Equal(t, NamedAgentPairingView{FallbackList: "mid"}, resp.Defaults.Agents["ChatAgent"])

		chain := resp.Chains["main"]
		assert.Equal(t, "premium", chain.FallbackList)
		require.NotNil(t, chain.Compose)
		assert.Equal(t, "langchain", chain.Compose.LLMBackend)
		assert.Equal(t, "mid", chain.Compose.FallbackList)
		require.NotNil(t, chain.ExecutiveSummary)
		assert.Equal(t, "mid", chain.ExecutiveSummary.FallbackList)
		require.Len(t, chain.FallbackProviders, 1)
		assert.Equal(t, "legacy-inline", chain.FallbackProviders[0].Provider)
		require.Len(t, chain.SubAgents, 1)
		assert.Equal(t, "empty", chain.SubAgents[0].FallbackList)
		require.NotNil(t, chain.Chat)
		assert.Equal(t, "mid", chain.Chat.FallbackList)
		require.NotNil(t, chain.Scoring)
		assert.Equal(t, "mid", chain.Scoring.FallbackList)
		require.Len(t, chain.Stages, 1)
		assert.Equal(t, "premium", chain.Stages[0].FallbackList)
		require.Len(t, chain.Stages[0].Agents, 1)
		assert.Equal(t, "premium", chain.Stages[0].Agents[0].FallbackList)
		require.Len(t, chain.Stages[0].Agents[0].SubAgents, 1)
		assert.Equal(t, "empty", chain.Stages[0].Agents[0].SubAgents[0].FallbackList)
		require.NotNil(t, chain.Stages[0].Synthesis)
		assert.Equal(t, "mid", chain.Stages[0].Synthesis.FallbackList)

		agentRaw, err := json.Marshal(resp.Agents)
		require.NoError(t, err)
		assert.NotContains(t, string(agentRaw), `"llm_provider"`)
		assert.NotContains(t, string(agentRaw), `"fallback_list"`)

		mcpSumRaw, err := json.Marshal(resp.MCPServers["k8s"].Summarization)
		require.NoError(t, err)
		assert.NotContains(t, string(mcpSumRaw), `"fallback_list"`)
		assert.NotContains(t, string(mcpSumRaw), "must-not-appear")

		emptyRaw, err := json.Marshal(resp.FallbackLists["empty"])
		require.NoError(t, err)
		assert.JSONEq(t, `[]`, string(emptyRaw))

		premiumRaw, err := json.Marshal(resp.FallbackLists["premium"][0])
		require.NoError(t, err)
		assert.JSONEq(t, `{"llm_provider":"claude-opus","llm_backend":"langchain"}`, string(premiumRaw))

		chainComposeRaw, err := json.Marshal(chain.Compose)
		require.NoError(t, err)
		assert.JSONEq(t, `{"llm_provider":"claude-sonnet","llm_backend":"langchain","fallback_list":"mid"}`, string(chainComposeRaw))

		chainRaw, err := json.Marshal(chain)
		require.NoError(t, err)
		chainBody := string(chainRaw)
		assert.NotContains(t, chainBody, `"compose_provider"`)
		assert.NotContains(t, chainBody, `"compose_backend"`)
		assert.NotContains(t, chainBody, `"compose_fallback_list"`)
		assert.NotContains(t, chainBody, `"executive_summary_provider"`)
		assert.NotContains(t, chainBody, `"executive_summary_backend"`)
		assert.NotContains(t, chainBody, `"executive_summary_fallback_list"`)
	})

	t.Run("empty catalogs emit JSON empty object", func(t *testing.T) {
		tests := []struct {
			name  string
			lists map[string][]config.FallbackProviderEntry
		}{
			{name: "nil"},
			{name: "empty map", lists: map[string][]config.FallbackProviderEntry{}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				resp := buildSystemConfigResponse(&config.Config{FallbackLists: tt.lists}, nil)
				require.NotNil(t, resp.FallbackLists)
				assert.Empty(t, resp.FallbackLists)

				raw, err := json.Marshal(resp)
				require.NoError(t, err)
				var decoded map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(raw, &decoded))
				assert.JSONEq(t, `{}`, string(decoded["fallback_lists"]))
			})
		}
	})

	t.Run("omitted selectors and sibling backends stay out of JSON", func(t *testing.T) {
		resp := buildSystemConfigResponse(&config.Config{
			Defaults: &config.Defaults{
				LLMProvider: "google-default",
				Compose:     &config.JobPairing{LLMProvider: "google-default"},
			},
			ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
				"plain": {
					AlertTypes: []string{"X"},
					Compose:    &config.JobPairing{},
					Stages: []config.StageConfig{
						{Name: "s", Agents: []config.StageAgentConfig{{Name: "A"}}},
					},
				},
			}),
		}, nil)

		rawDefaults, err := json.Marshal(resp.Defaults)
		require.NoError(t, err)
		defaultsBody := string(rawDefaults)
		assert.NotContains(t, defaultsBody, `"fallback_list"`)
		assert.NotContains(t, defaultsBody, `"llm_backend"`)
		assert.NotContains(t, defaultsBody, `"compose_provider"`)
		assert.NotContains(t, defaultsBody, `"executive_summary"`)
		assert.NotContains(t, defaultsBody, `"agents"`)
		require.NotNil(t, resp.Defaults.Compose)
		composeRaw, err := json.Marshal(resp.Defaults.Compose)
		require.NoError(t, err)
		assert.JSONEq(t, `{"llm_provider":"google-default"}`, string(composeRaw))

		chainRaw, err := json.Marshal(resp.Chains["plain"])
		require.NoError(t, err)
		chainBody := string(chainRaw)
		assert.NotContains(t, chainBody, `"fallback_list"`)
		assert.NotContains(t, chainBody, `"compose"`)
		assert.NotContains(t, chainBody, `"executive_summary"`)
	})
}

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "plain https", in: "https://api.example.com/v1", want: "https://api.example.com/v1"},
		{name: "strips userinfo query fragment", in: "https://user:pass@host.example/path?token=secret#x", want: "https://host.example/path"},
		{name: "unparseable secret-looking", in: "sk-FAKE-NOT-REAL-API-KEY-XXXXXXXXXXXX", want: "***"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeURL(tt.in))
		})
	}
}

func TestBuildOrchestratorView(t *testing.T) {
	t.Parallel()

	maxConcurrent := 5
	timeout := 2 * time.Minute

	t.Run("omits unset agent_timeout and has no max_budget", func(t *testing.T) {
		t.Parallel()
		view := buildOrchestratorView(&config.OrchestratorConfig{
			MaxConcurrentAgents: &maxConcurrent,
		})
		require.NotNil(t, view)
		assert.Equal(t, 5, *view.MaxConcurrentAgents)
		assert.Nil(t, view.AgentTimeout)

		raw, err := json.Marshal(view)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "agent_timeout")
		assert.NotContains(t, string(raw), "max_budget")
	})

	t.Run("emits set agent_timeout", func(t *testing.T) {
		t.Parallel()
		view := buildOrchestratorView(&config.OrchestratorConfig{
			MaxConcurrentAgents: &maxConcurrent,
			AgentTimeout:        &timeout,
		})
		require.NotNil(t, view)
		require.NotNil(t, view.AgentTimeout)
		assert.Equal(t, "2m", *view.AgentTimeout)
	})

	t.Run("nil config yields nil view", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, buildOrchestratorView(nil))
	})
}

func TestBuildChatView(t *testing.T) {
	t.Parallel()

	t.Run("nil config yields nil view", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, buildChatView(nil))
	})

	t.Run("omitted enabled is true and omitted agent stays empty", func(t *testing.T) {
		t.Parallel()
		view := buildChatView(&config.ChatConfig{LLMProvider: "google-default"})
		require.NotNil(t, view)
		assert.True(t, view.Enabled)
		assert.Empty(t, view.Agent)
		assert.Equal(t, "google-default", view.LLMProvider)
	})

	t.Run("explicit false stays disabled", func(t *testing.T) {
		t.Parallel()
		view := buildChatView(&config.ChatConfig{Enabled: config.BoolPtr(false)})
		require.NotNil(t, view)
		assert.False(t, view.Enabled)
	})
}

func TestSystemConfigSkillHandler(t *testing.T) {
	s := &Server{
		cfg: &config.Config{
			SkillRegistry: config.NewSkillRegistry(map[string]*config.SkillConfig{
				"my-skill": {
					Name:        "my-skill",
					Description: "desc",
					Body:        "# Skill body\n\nDo things.",
				},
			}),
		},
	}

	t.Run("returns skill body", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/config/skills/my-skill", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPathValues(echo.PathValues{{Name: "name", Value: "my-skill"}})

		err := s.systemConfigSkillHandler(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp SystemConfigSkillResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "my-skill", resp.Name)
		assert.Equal(t, "desc", resp.Description)
		assert.Equal(t, "# Skill body\n\nDo things.", resp.Body)
	})

	t.Run("missing skill returns 404", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/config/skills/missing", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPathValues(echo.PathValues{{Name: "name", Value: "missing"}})

		err := s.systemConfigSkillHandler(c)
		require.Error(t, err)
		var he *echo.HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, http.StatusNotFound, he.Code)
	})

	t.Run("nil skill registry returns 404", func(t *testing.T) {
		empty := &Server{cfg: &config.Config{}}
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/config/skills/x", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPathValues(echo.PathValues{{Name: "name", Value: "x"}})

		err := empty.systemConfigSkillHandler(c)
		require.Error(t, err)
		var he *echo.HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, http.StatusNotFound, he.Code)
	})

	t.Run("nil server config returns 404", func(t *testing.T) {
		empty := &Server{cfg: nil}
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/config/skills/x", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPathValues(echo.PathValues{{Name: "name", Value: "x"}})

		err := empty.systemConfigSkillHandler(c)
		require.Error(t, err)
		var he *echo.HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, http.StatusNotFound, he.Code)
	})

	t.Run("empty skill name returns 400", func(t *testing.T) {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/config/skills/", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPathValues(echo.PathValues{{Name: "name", Value: ""}})

		err := s.systemConfigSkillHandler(c)
		require.Error(t, err)
		var he *echo.HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, http.StatusBadRequest, he.Code)
	})
}
