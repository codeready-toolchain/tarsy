package masking

import (
	"fmt"
	"log/slog"
	"regexp"
	"slices"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// CompiledPattern holds a pre-compiled regex pattern with its replacement.
type CompiledPattern struct {
	Name        string
	Regex       *regexp.Regexp
	Replacement string
	Description string
}

// resolvedPatterns holds the resolved set of maskers and patterns for a masking operation.
type resolvedPatterns struct {
	codeMaskerNames []string           // Names of code-based maskers to apply
	regexPatterns   []*CompiledPattern // Compiled regex patterns to apply
}

// compileBuiltinPatterns compiles all built-in regex patterns from config.
// Invalid patterns are logged and skipped.
func (s *Service) compileBuiltinPatterns() {
	for name, pattern := range config.GetBuiltinConfig().MaskingPatterns {
		compiled, err := regexp.Compile(pattern.Pattern)
		if err != nil {
			slog.Error("Failed to compile built-in masking pattern, skipping",
				"pattern", name, "error", err)
			continue
		}
		s.patterns[name] = &CompiledPattern{
			Name:        name,
			Regex:       compiled,
			Replacement: pattern.Replacement,
			Description: pattern.Description,
		}
	}
}

// compileCustomPatterns compiles custom patterns from all MCP server configs.
// Custom patterns are keyed as "custom:{serverID}:{index}" to avoid collisions.
func (s *Service) compileCustomPatterns() {
	for serverID, serverCfg := range s.registry.GetAll() {
		if serverCfg.DataMasking == nil || !serverCfg.DataMasking.Enabled {
			continue
		}
		for i, pattern := range serverCfg.DataMasking.CustomPatterns {
			name := fmt.Sprintf("custom:%s:%d", serverID, i)
			compiled, err := regexp.Compile(pattern.Pattern)
			if err != nil {
				slog.Error("Failed to compile custom masking pattern, skipping",
					"pattern", name, "server", serverID, "error", err)
				continue
			}
			s.patterns[name] = &CompiledPattern{
				Name:        name,
				Regex:       compiled,
				Replacement: pattern.Replacement,
				Description: pattern.Description,
			}
			// Track which custom patterns belong to which server
			s.serverCustomPatterns[serverID] = append(s.serverCustomPatterns[serverID], name)
		}
	}
}

// resolvePatterns expands a MaskingConfig into a deduplicated resolvedPatterns.
func (s *Service) resolvePatterns(cfg *config.MaskingConfig, serverID string) *resolvedPatterns {
	seen := make(map[string]bool)
	resolved := &resolvedPatterns{}
	builtin := config.GetBuiltinConfig()

	// 1. Expand pattern_groups → individual pattern names
	for _, groupName := range cfg.PatternGroups {
		groupPatterns, ok := s.patternGroups[groupName]
		if !ok {
			continue
		}
		for _, name := range groupPatterns {
			if seen[name] {
				continue
			}
			seen[name] = true
			s.addToResolved(resolved, name, builtin)
		}
	}

	// 2. Add individual patterns from cfg.Patterns
	for _, name := range cfg.Patterns {
		if seen[name] {
			continue
		}
		seen[name] = true
		s.addToResolved(resolved, name, builtin)
	}

	// 3. Add custom patterns for this server
	if serverID != "" {
		for _, name := range s.serverCustomPatterns[serverID] {
			if seen[name] {
				continue
			}
			seen[name] = true
			if cp, ok := s.patterns[name]; ok {
				resolved.regexPatterns = append(resolved.regexPatterns, cp)
			}
		}
	}

	return resolved
}

// resolvePatternsFromGroup resolves a single pattern group name into resolvedPatterns.
func (s *Service) resolvePatternsFromGroup(groupName string) *resolvedPatterns {
	seen := make(map[string]bool)
	resolved := &resolvedPatterns{}
	builtin := config.GetBuiltinConfig()

	groupPatterns, ok := s.patternGroups[groupName]
	if !ok {
		return resolved
	}

	for _, name := range groupPatterns {
		if seen[name] {
			continue
		}
		seen[name] = true
		s.addToResolved(resolved, name, builtin)
	}

	return resolved
}

// addToResolved adds a pattern name to the resolved set, categorizing it as
// either a code masker or a regex pattern.
func (s *Service) addToResolved(resolved *resolvedPatterns, name string, builtin *config.BuiltinConfig) {
	// Check if it's a code-based masker
	if slices.Contains(builtin.CodeMaskers, name) {
		resolved.codeMaskerNames = append(resolved.codeMaskerNames, name)
		return
	}

	// Otherwise, look up in compiled regex patterns
	if cp, ok := s.patterns[name]; ok {
		resolved.regexPatterns = append(resolved.regexPatterns, cp)
	}
}
