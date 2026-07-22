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

	t.Run("http transport sets bearer_token_set and redacts url", func(t *testing.T) {
		verify := true
		got := sanitizeTransport(config.TransportConfig{
			Type:        config.TransportTypeHTTP,
			URL:         "https://mcp.example.com?token=live-secret",
			BearerToken: "live-bearer-token",
			VerifySSL:   &verify,
			Timeout:     30,
		})

		assert.Equal(t, "http", got.Type)
		assert.Equal(t, "***", got.URL)
		assert.True(t, got.BearerTokenSet)
		assert.Equal(t, &verify, got.VerifySSL)
		assert.Equal(t, 30, got.Timeout)
		assert.Empty(t, got.Command)
		assert.Nil(t, got.Args)

		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "live-secret")
		assert.NotContains(t, string(raw), "live-bearer-token")
		assert.Contains(t, string(raw), `"bearer_token_set":true`)
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
						Type:                config.LLMProviderTypeGoogle,
						Model:               "gemini-2.5-pro",
						APIKeyEnv:           "GOOGLE_API_KEY",
						MaxToolResultTokens: 150000,
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

		resp := buildSystemConfigResponse(s.cfg)
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
		assert.Empty(t, resp.Agents)
		assert.Empty(t, resp.Chains)
		assert.Empty(t, resp.MCPServers)
	})

	t.Run("includes chains defaults memory and orchestrator duration strings", func(t *testing.T) {
		maxIter := 10
		maxConcurrent := 3
		agentTimeout := 2 * time.Minute
		maxBudget := 30 * time.Minute
		emptySkills := []string{}

		s := &Server{
			cfg: &config.Config{
				Defaults: &config.Defaults{
					LLMProvider:   "google-default",
					MaxIterations: &maxIter,
					LLMBackend:    config.LLMBackendNativeGemini,
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
						MaxBudget:           &maxBudget,
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
						Stages: []config.StageConfig{
							{
								Name: "investigate",
								Agents: []config.StageAgentConfig{
									{Name: "Worker", LLMProvider: "google-default"},
								},
							},
						},
						Chat: &config.ChatConfig{Enabled: true, Agent: "Worker"},
					},
				}),
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"google-default": {
						Type:                config.LLMProviderTypeGoogle,
						Model:               "gemini-2.5-pro",
						APIKeyEnv:           "GOOGLE_API_KEY",
						BaseURL:             "https://generativelanguage.googleapis.com",
						MaxToolResultTokens: 150000,
					},
				}),
			},
		}

		resp := buildSystemConfigResponse(s.cfg)

		require.NotNil(t, resp.Defaults)
		assert.Equal(t, "google-default", resp.Defaults.LLMProvider)
		require.NotNil(t, resp.Defaults.Memory)
		assert.Equal(t, "GOOGLE_API_KEY", resp.Defaults.Memory.Embedding.APIKeyEnv)
		require.NotNil(t, resp.Defaults.Orchestrator)
		require.NotNil(t, resp.Defaults.Orchestrator.AgentTimeout)
		assert.Equal(t, "2m", *resp.Defaults.Orchestrator.AgentTimeout)
		require.NotNil(t, resp.Defaults.Orchestrator.MaxBudget)
		assert.Equal(t, "30m", *resp.Defaults.Orchestrator.MaxBudget)

		assert.Equal(t, []string{"alpha-chain", "zeta-chain"}, sortedKeys(resp.Chains))
		alpha := resp.Chains["alpha-chain"]
		assert.Equal(t, []string{"AlphaAlert"}, alpha.AlertTypes)
		assert.Equal(t, "google-default", alpha.LLMProvider)
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
