package api

import (
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// --- Response DTOs (allowlist; snake_case JSON) ---

// SystemConfigResponse is returned by GET /api/v1/system/config.
type SystemConfigResponse struct {
	Defaults     *DefaultsView              `json:"defaults"`
	Queue        *QueueView                 `json:"queue"`
	System       SystemView                 `json:"system"`
	Agents       map[string]AgentView       `json:"agents"`
	Chains       map[string]ChainView       `json:"chains"`
	MCPServers   map[string]MCPServerView   `json:"mcp_servers"`
	LLMProviders map[string]LLMProviderView `json:"llm_providers"`
	Skills       map[string]SkillMetaView   `json:"skills"`
}

// SystemConfigSkillResponse is returned by GET /api/v1/system/config/skills/:name.
type SystemConfigSkillResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// SanitizedTransport is the fail-closed MCP transport allowlist.
type SanitizedTransport struct {
	Type           string   `json:"type"`
	Command        string   `json:"command,omitempty"`
	Args           []string `json:"args,omitempty"`
	URL            string   `json:"url,omitempty"`
	VerifySSL      *bool    `json:"verify_ssl,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	EnvKeys        []string `json:"env_keys,omitempty"`
	BearerTokenSet bool     `json:"bearer_token_set"`
}

// MCPServerView is a sanitized MCP server config entry.
type MCPServerView struct {
	Transport     SanitizedTransport          `json:"transport"`
	Instructions  string                      `json:"instructions,omitempty"`
	DataMasking   *config.MaskingConfig       `json:"data_masking,omitempty"`
	Summarization *config.SummarizationConfig `json:"summarization,omitempty"`
}

// AgentView is the agent config view (no llm_provider field).
type AgentView struct {
	Type               string            `json:"type,omitempty"`
	Description        string            `json:"description,omitempty"`
	MCPServers         []string          `json:"mcp_servers,omitempty"`
	CustomInstructions string            `json:"custom_instructions"`
	LLMBackend         string            `json:"llm_backend,omitempty"`
	MaxIterations      *int              `json:"max_iterations,omitempty"`
	NativeTools        map[string]bool   `json:"native_tools,omitempty"`
	Orchestrator       *OrchestratorView `json:"orchestrator"`
	Skills             *[]string         `json:"skills"`
	RequiredSkills     []string          `json:"required_skills,omitempty"`
}

// OrchestratorView emits duration fields as strings.
type OrchestratorView struct {
	MaxConcurrentAgents *int    `json:"max_concurrent_agents,omitempty"`
	AgentTimeout        *string `json:"agent_timeout,omitempty"`
	MaxBudget           *string `json:"max_budget,omitempty"`
}

// ChainView is the chain config view.
type ChainView struct {
	AlertTypes               []string               `json:"alert_types"`
	Description              string                 `json:"description,omitempty"`
	Stages                   []StageView            `json:"stages"`
	Chat                     *ChatView              `json:"chat,omitempty"`
	Scoring                  *ScoringView           `json:"scoring,omitempty"`
	LLMProvider              string                 `json:"llm_provider,omitempty"`
	ExecutiveSummaryProvider string                 `json:"executive_summary_provider,omitempty"`
	LLMBackend               string                 `json:"llm_backend,omitempty"`
	FallbackProviders        []FallbackProviderView `json:"fallback_providers,omitempty"`
	MaxIterations            *int                   `json:"max_iterations,omitempty"`
	MCPServers               []string               `json:"mcp_servers,omitempty"`
	SubAgents                []SubAgentView         `json:"sub_agents,omitempty"`
}

// StageView is a chain stage.
type StageView struct {
	Name              string                 `json:"name"`
	Agents            []StageAgentView       `json:"agents"`
	Replicas          int                    `json:"replicas,omitempty"`
	SuccessPolicy     string                 `json:"success_policy,omitempty"`
	MaxIterations     *int                   `json:"max_iterations,omitempty"`
	MCPServers        []string               `json:"mcp_servers,omitempty"`
	FallbackProviders []FallbackProviderView `json:"fallback_providers,omitempty"`
	SubAgents         []SubAgentView         `json:"sub_agents,omitempty"`
	Synthesis         *SynthesisView         `json:"synthesis,omitempty"`
}

// StageAgentView is a stage agent reference with overrides.
type StageAgentView struct {
	Name              string                 `json:"name"`
	Type              string                 `json:"type,omitempty"`
	LLMProvider       string                 `json:"llm_provider,omitempty"`
	LLMBackend        string                 `json:"llm_backend,omitempty"`
	MaxIterations     *int                   `json:"max_iterations,omitempty"`
	MCPServers        []string               `json:"mcp_servers,omitempty"`
	SubAgents         []SubAgentView         `json:"sub_agents,omitempty"`
	FallbackProviders []FallbackProviderView `json:"fallback_providers,omitempty"`
	RequiredSkills    []string               `json:"required_skills,omitempty"`
	Skills            []string               `json:"skills,omitempty"`
}

// SubAgentView is a sub-agent reference.
type SubAgentView struct {
	Name           string   `json:"name"`
	LLMProvider    string   `json:"llm_provider,omitempty"`
	LLMBackend     string   `json:"llm_backend,omitempty"`
	MaxIterations  *int     `json:"max_iterations,omitempty"`
	MCPServers     []string `json:"mcp_servers,omitempty"`
	RequiredSkills []string `json:"required_skills,omitempty"`
	Skills         []string `json:"skills,omitempty"`
}

// FallbackProviderView is a fallback provider entry.
type FallbackProviderView struct {
	Provider string `json:"provider"`
	Backend  string `json:"backend"`
}

// SynthesisView is stage synthesis config.
type SynthesisView struct {
	Agent       string `json:"agent,omitempty"`
	LLMBackend  string `json:"llm_backend,omitempty"`
	LLMProvider string `json:"llm_provider,omitempty"`
}

// ChatView is chain chat config.
type ChatView struct {
	Enabled       bool           `json:"enabled"`
	Agent         string         `json:"agent,omitempty"`
	LLMBackend    string         `json:"llm_backend,omitempty"`
	LLMProvider   string         `json:"llm_provider,omitempty"`
	MCPServers    []string       `json:"mcp_servers,omitempty"`
	MaxIterations *int           `json:"max_iterations,omitempty"`
	SubAgents     []SubAgentView `json:"sub_agents,omitempty"`
}

// ScoringView is scoring config.
type ScoringView struct {
	Enabled       bool     `json:"enabled"`
	Agent         string   `json:"agent,omitempty"`
	LLMBackend    string   `json:"llm_backend,omitempty"`
	LLMProvider   string   `json:"llm_provider,omitempty"`
	MCPServers    []string `json:"mcp_servers,omitempty"`
	MaxIterations *int     `json:"max_iterations,omitempty"`
}

// LLMProviderView is an LLM provider config entry.
type LLMProviderView struct {
	Type                string          `json:"type"`
	Model               string          `json:"model"`
	APIKeyEnv           string          `json:"api_key_env,omitempty"`
	CredentialsEnv      string          `json:"credentials_env,omitempty"`
	ProjectEnv          string          `json:"project_env,omitempty"`
	LocationEnv         string          `json:"location_env,omitempty"`
	BaseURL             string          `json:"base_url,omitempty"`
	MaxToolResultTokens int             `json:"max_tool_result_tokens"`
	NativeTools         map[string]bool `json:"native_tools,omitempty"`
}

// SkillMetaView is skill metadata (no body).
type SkillMetaView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// DefaultsView is system-wide defaults.
type DefaultsView struct {
	LLMProvider       string                 `json:"llm_provider,omitempty"`
	MaxIterations     *int                   `json:"max_iterations,omitempty"`
	LLMBackend        string                 `json:"llm_backend,omitempty"`
	FallbackProviders []FallbackProviderView `json:"fallback_providers,omitempty"`
	Scoring           *ScoringView           `json:"scoring,omitempty"`
	SuccessPolicy     string                 `json:"success_policy,omitempty"`
	AlertType         string                 `json:"alert_type,omitempty"`
	Runbook           string                 `json:"runbook,omitempty"`
	AlertMasking      *AlertMaskingView      `json:"alert_masking,omitempty"`
	Orchestrator      *OrchestratorView      `json:"orchestrator,omitempty"`
	Memory            *MemoryView            `json:"memory,omitempty"`
}

// AlertMaskingView is alert masking defaults.
type AlertMaskingView struct {
	Enabled      bool   `json:"enabled"`
	PatternGroup string `json:"pattern_group"`
}

// MemoryView is investigation memory config.
type MemoryView struct {
	Enabled              bool          `json:"enabled"`
	MaxInject            int           `json:"max_inject,omitempty"`
	ReflectorMemoryLimit int           `json:"reflector_memory_limit,omitempty"`
	Embedding            EmbeddingView `json:"embedding,omitempty"`
}

// EmbeddingView is embedding model config (api_key_env name only).
type EmbeddingView struct {
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	APIKeyEnv  string `json:"api_key_env,omitempty"`
	Dimensions int    `json:"dimensions,omitempty"`
	BaseURL    string `json:"base_url,omitempty"`
}

// QueueView emits all queue durations as strings.
type QueueView struct {
	WorkerCount             int    `json:"worker_count"`
	MaxConcurrentSessions   int    `json:"max_concurrent_sessions"`
	PollInterval            string `json:"poll_interval"`
	PollIntervalJitter      string `json:"poll_interval_jitter"`
	SessionTimeout          string `json:"session_timeout"`
	GracefulShutdownTimeout string `json:"graceful_shutdown_timeout"`
	ScoringShutdownTimeout  string `json:"scoring_shutdown_timeout"`
	OrphanDetectionInterval string `json:"orphan_detection_interval"`
	OrphanThreshold         string `json:"orphan_threshold"`
	HeartbeatInterval       string `json:"heartbeat_interval"`
}

// SystemView is GitHub/Slack/runbooks/retention/dashboard settings.
type SystemView struct {
	GitHub           *GitHubView    `json:"github,omitempty"`
	Slack            *SlackView     `json:"slack,omitempty"`
	Runbooks         *RunbooksView  `json:"runbooks,omitempty"`
	Retention        *RetentionView `json:"retention,omitempty"`
	DashboardURL     string         `json:"dashboard_url,omitempty"`
	AllowedWSOrigins []string       `json:"allowed_ws_origins"`
}

// GitHubView shows token env name only.
type GitHubView struct {
	TokenEnv string `json:"token_env,omitempty"`
}

// SlackView shows token env name only.
type SlackView struct {
	Enabled  bool   `json:"enabled"`
	TokenEnv string `json:"token_env,omitempty"`
	Channel  string `json:"channel,omitempty"`
}

// RunbooksView is runbook system config.
type RunbooksView struct {
	RepoURL        string   `json:"repo_url,omitempty"`
	CacheTTL       string   `json:"cache_ttl,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
}

// RetentionView emits durations as strings.
type RetentionView struct {
	SessionRetentionDays int    `json:"session_retention_days"`
	EventTTL             string `json:"event_ttl"`
	CleanupInterval      string `json:"cleanup_interval"`
}

// --- Builder ---

func buildSystemConfigResponse(cfg *config.Config) SystemConfigResponse {
	resp := SystemConfigResponse{
		Agents:       map[string]AgentView{},
		Chains:       map[string]ChainView{},
		MCPServers:   map[string]MCPServerView{},
		LLMProviders: map[string]LLMProviderView{},
		Skills:       map[string]SkillMetaView{},
		System: SystemView{
			AllowedWSOrigins: []string{},
		},
	}
	if cfg == nil {
		return resp
	}

	resp.Defaults = buildDefaultsView(cfg.Defaults)
	resp.Queue = buildQueueView(cfg.Queue)
	resp.System = buildSystemView(cfg)

	if cfg.AgentRegistry != nil {
		agents := cfg.AgentRegistry.GetAll()
		resp.Agents = make(map[string]AgentView, len(agents))
		for _, name := range sortedKeys(agents) {
			resp.Agents[name] = buildAgentView(agents[name])
		}
	}

	if cfg.ChainRegistry != nil {
		chains := cfg.ChainRegistry.GetAll()
		resp.Chains = make(map[string]ChainView, len(chains))
		for _, id := range sortedKeys(chains) {
			resp.Chains[id] = buildChainView(chains[id])
		}
	}

	if cfg.MCPServerRegistry != nil {
		servers := cfg.MCPServerRegistry.GetAll()
		resp.MCPServers = make(map[string]MCPServerView, len(servers))
		for _, id := range sortedKeys(servers) {
			resp.MCPServers[id] = buildMCPServerView(servers[id])
		}
	}

	if cfg.LLMProviderRegistry != nil {
		providers := cfg.LLMProviderRegistry.GetAll()
		resp.LLMProviders = make(map[string]LLMProviderView, len(providers))
		for _, id := range sortedKeys(providers) {
			resp.LLMProviders[id] = buildLLMProviderView(providers[id])
		}
	}

	if cfg.SkillRegistry != nil {
		skills := cfg.SkillRegistry.GetAll()
		resp.Skills = make(map[string]SkillMetaView, len(skills))
		for _, name := range sortedKeys(skills) {
			s := skills[name]
			resp.Skills[name] = SkillMetaView{
				Name:        s.Name,
				Description: s.Description,
			}
		}
	}

	return resp
}

func buildDefaultsView(d *config.Defaults) *DefaultsView {
	if d == nil {
		return nil
	}
	view := &DefaultsView{
		LLMProvider:       d.LLMProvider,
		MaxIterations:     d.MaxIterations,
		LLMBackend:        string(d.LLMBackend),
		FallbackProviders: buildFallbackProviders(d.FallbackProviders),
		Scoring:           buildScoringView(d.Scoring),
		SuccessPolicy:     string(d.SuccessPolicy),
		AlertType:         d.AlertType,
		Runbook:           d.Runbook,
		Orchestrator:      buildOrchestratorView(d.Orchestrator),
	}
	if d.AlertMasking != nil {
		view.AlertMasking = &AlertMaskingView{
			Enabled:      d.AlertMasking.Enabled,
			PatternGroup: d.AlertMasking.PatternGroup,
		}
	}
	if d.Memory != nil {
		view.Memory = &MemoryView{
			Enabled:              d.Memory.Enabled,
			MaxInject:            d.Memory.MaxInject,
			ReflectorMemoryLimit: d.Memory.ReflectorMemoryLimit,
			Embedding: EmbeddingView{
				Provider:   string(d.Memory.Embedding.Provider),
				Model:      d.Memory.Embedding.Model,
				APIKeyEnv:  d.Memory.Embedding.APIKeyEnv,
				Dimensions: d.Memory.Embedding.Dimensions,
				BaseURL:    d.Memory.Embedding.BaseURL,
			},
		}
	}
	return view
}

func buildQueueView(q *config.QueueConfig) *QueueView {
	if q == nil {
		return nil
	}
	return &QueueView{
		WorkerCount:             q.WorkerCount,
		MaxConcurrentSessions:   q.MaxConcurrentSessions,
		PollInterval:            durationString(q.PollInterval),
		PollIntervalJitter:      durationString(q.PollIntervalJitter),
		SessionTimeout:          durationString(q.SessionTimeout),
		GracefulShutdownTimeout: durationString(q.GracefulShutdownTimeout),
		ScoringShutdownTimeout:  durationString(q.ScoringShutdownTimeout),
		OrphanDetectionInterval: durationString(q.OrphanDetectionInterval),
		OrphanThreshold:         durationString(q.OrphanThreshold),
		HeartbeatInterval:       durationString(q.HeartbeatInterval),
	}
}

func buildSystemView(cfg *config.Config) SystemView {
	view := SystemView{
		DashboardURL:     cfg.DashboardURL,
		AllowedWSOrigins: cfg.AllowedWSOrigins,
	}
	if view.AllowedWSOrigins == nil {
		view.AllowedWSOrigins = []string{}
	}
	if cfg.GitHub != nil {
		view.GitHub = &GitHubView{TokenEnv: cfg.GitHub.TokenEnv}
	}
	if cfg.Slack != nil {
		view.Slack = &SlackView{
			Enabled:  cfg.Slack.Enabled,
			TokenEnv: cfg.Slack.TokenEnv,
			Channel:  cfg.Slack.Channel,
		}
	}
	if cfg.Runbooks != nil {
		view.Runbooks = &RunbooksView{
			RepoURL:        cfg.Runbooks.RepoURL,
			CacheTTL:       durationString(cfg.Runbooks.CacheTTL),
			AllowedDomains: cfg.Runbooks.AllowedDomains,
		}
	}
	if cfg.Retention != nil {
		view.Retention = &RetentionView{
			SessionRetentionDays: cfg.Retention.SessionRetentionDays,
			EventTTL:             durationString(cfg.Retention.EventTTL),
			CleanupInterval:      durationString(cfg.Retention.CleanupInterval),
		}
	}
	return view
}

func buildAgentView(a *config.AgentConfig) AgentView {
	if a == nil {
		return AgentView{}
	}
	return AgentView{
		Type:               string(a.Type),
		Description:        a.Description,
		MCPServers:         a.MCPServers,
		CustomInstructions: a.CustomInstructions,
		LLMBackend:         string(a.LLMBackend),
		MaxIterations:      a.MaxIterations,
		NativeTools:        nativeToolsToMap(a.NativeTools),
		Orchestrator:       buildOrchestratorView(a.Orchestrator),
		Skills:             a.Skills,
		RequiredSkills:     a.RequiredSkills,
	}
}

func buildOrchestratorView(o *config.OrchestratorConfig) *OrchestratorView {
	if o == nil {
		return nil
	}
	view := &OrchestratorView{
		MaxConcurrentAgents: o.MaxConcurrentAgents,
	}
	if o.AgentTimeout != nil {
		s := durationString(*o.AgentTimeout)
		view.AgentTimeout = &s
	}
	if o.MaxBudget != nil {
		s := durationString(*o.MaxBudget)
		view.MaxBudget = &s
	}
	return view
}

func buildChainView(c *config.ChainConfig) ChainView {
	if c == nil {
		return ChainView{}
	}
	stages := make([]StageView, 0, len(c.Stages))
	for _, st := range c.Stages {
		stages = append(stages, buildStageView(st))
	}
	return ChainView{
		AlertTypes:               c.AlertTypes,
		Description:              c.Description,
		Stages:                   stages,
		Chat:                     buildChatView(c.Chat),
		Scoring:                  buildScoringView(c.Scoring),
		LLMProvider:              c.LLMProvider,
		ExecutiveSummaryProvider: c.ExecutiveSummaryProvider,
		LLMBackend:               string(c.LLMBackend),
		FallbackProviders:        buildFallbackProviders(c.FallbackProviders),
		MaxIterations:            c.MaxIterations,
		MCPServers:               c.MCPServers,
		SubAgents:                buildSubAgentViews(c.SubAgents),
	}
}

func buildStageView(st config.StageConfig) StageView {
	agents := make([]StageAgentView, 0, len(st.Agents))
	for _, a := range st.Agents {
		agents = append(agents, StageAgentView{
			Name:              a.Name,
			Type:              string(a.Type),
			LLMProvider:       a.LLMProvider,
			LLMBackend:        string(a.LLMBackend),
			MaxIterations:     a.MaxIterations,
			MCPServers:        a.MCPServers,
			SubAgents:         buildSubAgentViews(a.SubAgents),
			FallbackProviders: buildFallbackProviders(a.FallbackProviders),
			RequiredSkills:    a.RequiredSkills,
			Skills:            a.Skills,
		})
	}
	var synthesis *SynthesisView
	if st.Synthesis != nil {
		synthesis = &SynthesisView{
			Agent:       st.Synthesis.Agent,
			LLMBackend:  string(st.Synthesis.LLMBackend),
			LLMProvider: st.Synthesis.LLMProvider,
		}
	}
	return StageView{
		Name:              st.Name,
		Agents:            agents,
		Replicas:          st.Replicas,
		SuccessPolicy:     string(st.SuccessPolicy),
		MaxIterations:     st.MaxIterations,
		MCPServers:        st.MCPServers,
		FallbackProviders: buildFallbackProviders(st.FallbackProviders),
		SubAgents:         buildSubAgentViews(st.SubAgents),
		Synthesis:         synthesis,
	}
}

func buildChatView(c *config.ChatConfig) *ChatView {
	if c == nil {
		return nil
	}
	return &ChatView{
		Enabled:       c.Enabled,
		Agent:         c.Agent,
		LLMBackend:    string(c.LLMBackend),
		LLMProvider:   c.LLMProvider,
		MCPServers:    c.MCPServers,
		MaxIterations: c.MaxIterations,
		SubAgents:     buildSubAgentViews(c.SubAgents),
	}
}

func buildScoringView(s *config.ScoringConfig) *ScoringView {
	if s == nil {
		return nil
	}
	return &ScoringView{
		Enabled:       s.Enabled,
		Agent:         s.Agent,
		LLMBackend:    string(s.LLMBackend),
		LLMProvider:   s.LLMProvider,
		MCPServers:    s.MCPServers,
		MaxIterations: s.MaxIterations,
	}
}

func buildSubAgentViews(refs config.SubAgentRefs) []SubAgentView {
	if refs == nil {
		return nil
	}
	out := make([]SubAgentView, 0, len(refs))
	for _, r := range refs {
		out = append(out, SubAgentView{
			Name:           r.Name,
			LLMProvider:    r.LLMProvider,
			LLMBackend:     string(r.LLMBackend),
			MaxIterations:  r.MaxIterations,
			MCPServers:     r.MCPServers,
			RequiredSkills: r.RequiredSkills,
			Skills:         r.Skills,
		})
	}
	return out
}

func buildFallbackProviders(entries []config.FallbackProviderEntry) []FallbackProviderView {
	if entries == nil {
		return nil
	}
	out := make([]FallbackProviderView, 0, len(entries))
	for _, e := range entries {
		out = append(out, FallbackProviderView{
			Provider: e.Provider,
			Backend:  string(e.Backend),
		})
	}
	return out
}

func buildMCPServerView(s *config.MCPServerConfig) MCPServerView {
	if s == nil {
		return MCPServerView{}
	}
	return MCPServerView{
		Transport:     sanitizeTransport(s.Transport),
		Instructions:  s.Instructions,
		DataMasking:   s.DataMasking,
		Summarization: s.Summarization,
	}
}

func buildLLMProviderView(p *config.LLMProviderConfig) LLMProviderView {
	if p == nil {
		return LLMProviderView{}
	}
	return LLMProviderView{
		Type:                string(p.Type),
		Model:               p.Model,
		APIKeyEnv:           p.APIKeyEnv,
		CredentialsEnv:      p.CredentialsEnv,
		ProjectEnv:          p.ProjectEnv,
		LocationEnv:         p.LocationEnv,
		BaseURL:             sanitizeURL(p.BaseURL),
		MaxToolResultTokens: p.MaxToolResultTokens,
		NativeTools:         nativeToolsToMap(p.NativeTools),
	}
}

// sanitizeTransport builds the fail-closed transport allowlist DTO.
func sanitizeTransport(t config.TransportConfig) SanitizedTransport {
	out := SanitizedTransport{
		Type:           string(t.Type),
		VerifySSL:      t.VerifySSL,
		Timeout:        t.Timeout,
		BearerTokenSet: t.BearerToken != "",
	}

	if t.Command != "" {
		if looksSecretBearing(t.Command) {
			out.Command = "***"
		} else {
			out.Command = t.Command
		}
	}

	if len(t.Args) > 0 {
		out.Args = []string{"***"}
	}
	if t.URL != "" {
		out.URL = sanitizeURL(t.URL)
	}

	if len(t.Env) > 0 {
		keys := slices.Collect(maps.Keys(t.Env))
		slices.Sort(keys)
		out.EnvKeys = keys
	}

	return out
}

// sanitizeURL returns a safe URL representation: scheme, host, port, and path
// are preserved; userinfo, query, and fragment (common secret carriers after
// ExpandEnv) are stripped. Unparseable secret-looking values become "***".
func sanitizeURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if looksSecretBearing(raw) {
			return "***"
		}
		return raw
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

var (
	// credentialPrefixes matches known secret prefixes (case-sensitive where typical).
	credentialPrefixes = []string{
		"ghp_", "gho_", "github_pat_", "xoxb-", "xoxp-", "sk-", "AKIA", "Bearer ",
	}
	// jwtLike matches JWT-shaped strings (header.payload.signature).
	jwtLike = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	// longTokenLike matches ≥32 contiguous token-like characters.
	// Path separators (/) are excluded so ordinary binary paths are not redacted.
	longTokenLike = regexp.MustCompile(`[A-Za-z0-9_+=\-]{32,}`)
)

// looksSecretBearing reports whether s looks like it embeds a live secret.
// Intentionally narrow; false positives redact to "***" which is acceptable.
func looksSecretBearing(s string) bool {
	if s == "" {
		return false
	}
	for _, prefix := range credentialPrefixes {
		if hasCredentialPrefix(s, prefix) {
			return true
		}
	}
	if jwtLike.MatchString(s) {
		return true
	}
	// Long high-entropy substrings atypical for a binary path.
	if longTokenLike.MatchString(s) {
		return true
	}
	return false
}

// hasCredentialPrefix reports whether prefix appears at a token boundary
// (start of string or after a non-alphanumeric character), so values like
// "risk-management-tool" do not match "sk-".
func hasCredentialPrefix(s, prefix string) bool {
	for idx := 0; idx <= len(s)-len(prefix); {
		i := strings.Index(s[idx:], prefix)
		if i < 0 {
			return false
		}
		abs := idx + i
		if abs == 0 || !isASCIIAlphaNum(s[abs-1]) {
			return true
		}
		idx = abs + 1
	}
	return false
}

func isASCIIAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// durationString emits durations without trailing zero-valued units
// (e.g. "40m", "5s") while preserving meaningful compound units ("1h30m").
func durationString(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	s := d.String()
	for {
		next := trimTrailingZeroUnit(s, "s")
		next = trimTrailingZeroUnit(next, "m")
		next = trimTrailingZeroUnit(next, "h")
		if next == s {
			return s
		}
		s = next
	}
}

// trimTrailingZeroUnit removes a trailing "0{unit}" only when the 0 is a full
// unit value (preceded by a non-digit), so "40m0s" → "40m" but "40m" stays.
func trimTrailingZeroUnit(s, unit string) string {
	suffix := "0" + unit
	if !strings.HasSuffix(s, suffix) || len(s) == len(suffix) {
		return s
	}
	before := s[len(s)-len(suffix)-1]
	if before >= '0' && before <= '9' {
		return s
	}
	return s[:len(s)-len(suffix)]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := slices.Collect(maps.Keys(m))
	slices.Sort(keys)
	return keys
}

func nativeToolsToMap(tools map[config.GoogleNativeTool]bool) map[string]bool {
	if tools == nil {
		return nil
	}
	out := make(map[string]bool, len(tools))
	for k, v := range tools {
		out[string(k)] = v
	}
	return out
}
