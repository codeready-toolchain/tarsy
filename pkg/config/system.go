package config

import "time"

// GitHubConfig holds resolved GitHub integration configuration.
type GitHubConfig struct {
	TokenEnv string // Env var name containing GitHub PAT (default: "GITHUB_TOKEN")
}

// RunbookConfig holds resolved runbook system configuration.
type RunbookConfig struct {
	RepoURL        string        // GitHub repo URL for listing runbooks (empty = disabled)
	CacheTTL       time.Duration // Cache duration (default: 1m)
	AllowedDomains []string      // Allowed URL domains (default: ["github.com", "raw.githubusercontent.com"])
}

// SlackConfig holds resolved Slack notification configuration.
type SlackConfig struct {
	Enabled  bool
	TokenEnv string // Env var name for Slack bot token (default: "SLACK_BOT_TOKEN")
	Channel  string // Slack channel ID (e.g., "C12345678")
}
