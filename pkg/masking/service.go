package masking

import (
	"log/slog"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// AlertMaskingConfig holds alert payload masking settings.
type AlertMaskingConfig struct {
	Enabled      bool
	PatternGroup string
}

// MaskingService applies data masking to MCP tool results and alert payloads.
// Created once at application startup (singleton). Thread-safe and stateless
// aside from compiled patterns.
type MaskingService struct {
	registry             *config.MCPServerRegistry
	patterns             map[string]*CompiledPattern // Built-in + custom compiled patterns
	patternGroups        map[string][]string         // Group name → pattern names
	codeMaskers          map[string]Masker           // Registered code-based maskers
	alertMasking         AlertMaskingConfig          // Alert payload masking settings
	serverCustomPatterns map[string][]string         // serverID → custom pattern keys
}

// NewMaskingService creates a masking service with compiled patterns and registered maskers.
// All patterns are compiled eagerly at creation time. Invalid patterns are logged and skipped.
func NewMaskingService(
	registry *config.MCPServerRegistry,
	alertCfg AlertMaskingConfig,
) *MaskingService {
	s := &MaskingService{
		registry:             registry,
		patterns:             make(map[string]*CompiledPattern),
		patternGroups:        config.GetBuiltinConfig().PatternGroups,
		codeMaskers:          make(map[string]Masker),
		alertMasking:         alertCfg,
		serverCustomPatterns: make(map[string][]string),
	}

	// 1. Compile all built-in regex patterns
	s.compileBuiltinPatterns()

	// 2. Compile custom patterns from all MCP server configs
	s.compileCustomPatterns()

	// 3. Register code-based maskers
	s.registerMasker(&KubernetesSecretMasker{})

	slog.Info("Masking service initialized",
		"builtin_patterns", len(config.GetBuiltinConfig().MaskingPatterns),
		"compiled_patterns", len(s.patterns),
		"code_maskers", len(s.codeMaskers),
		"alert_masking_enabled", alertCfg.Enabled)

	return s
}

// MaskToolResult applies server-specific masking to MCP tool result content.
// Returns masked content. On masking failure, returns a redaction notice (fail-closed).
func (s *MaskingService) MaskToolResult(content string, serverID string) string {
	if content == "" {
		return content
	}

	// Look up server masking config
	serverCfg, err := s.registry.Get(serverID)
	if err != nil || serverCfg.DataMasking == nil || !serverCfg.DataMasking.Enabled {
		return content // No masking configured
	}

	// Resolve patterns for this server
	resolved := s.resolvePatterns(serverCfg.DataMasking, serverID)
	if len(resolved.codeMaskerNames) == 0 && len(resolved.regexPatterns) == 0 {
		return content
	}

	// Apply masking with fail-closed error handling
	masked, err := s.applyMasking(content, resolved)
	if err != nil {
		slog.Error("Masking failed, redacting content (fail-closed)",
			"server", serverID, "error", err)
		return "[REDACTED: data masking failure — tool result could not be safely processed]"
	}

	return masked
}

// MaskAlertData applies masking to alert payload data using the configured pattern group.
// Returns masked data. On masking failure, returns original data (fail-open for alerts).
func (s *MaskingService) MaskAlertData(data string) string {
	if !s.alertMasking.Enabled || data == "" {
		return data
	}

	resolved := s.resolvePatternsFromGroup(s.alertMasking.PatternGroup)
	if len(resolved.codeMaskerNames) == 0 && len(resolved.regexPatterns) == 0 {
		return data
	}

	masked, err := s.applyMasking(data, resolved)
	if err != nil {
		slog.Error("Alert masking failed, continuing with unmasked data (fail-open)",
			"error", err)
		return data
	}

	return masked
}

// applyMasking applies code-based maskers then regex patterns to content.
func (s *MaskingService) applyMasking(content string, resolved *resolvedPatterns) (string, error) {
	masked := content

	// Phase 1: Code-based maskers (more specific, structural awareness)
	for _, maskerName := range resolved.codeMaskerNames {
		masker, ok := s.codeMaskers[maskerName]
		if !ok {
			continue
		}
		if masker.AppliesTo(masked) {
			masked = masker.Mask(masked)
		}
	}

	// Phase 2: Regex patterns (general sweep)
	for _, pattern := range resolved.regexPatterns {
		masked = pattern.Regex.ReplaceAllString(masked, pattern.Replacement)
	}

	return masked, nil
}

// registerMasker registers a code-based masker by its name.
func (s *MaskingService) registerMasker(m Masker) {
	s.codeMaskers[m.Name()] = m
}
