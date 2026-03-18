package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// skillFrontmatter is the YAML frontmatter structure in SKILL.md files.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// LoadSkills scans configDir/skills/*/SKILL.md, parses each file,
// and returns a SkillRegistry. Returns an empty registry if the skills/
// directory doesn't exist.
func LoadSkills(configDir string) (*SkillRegistry, error) {
	skillsDir := filepath.Join(configDir, "skills")

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewSkillRegistry(nil), nil
		}
		return nil, fmt.Errorf("read skills directory: %w", err)
	}

	skills := make(map[string]*SkillConfig)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		skill, err := parseSkillFile(skillPath)
		if err != nil {
			return nil, NewLoadError(skillPath, err)
		}
		if skill == nil {
			continue
		}

		if _, exists := skills[skill.Name]; exists {
			return nil, NewLoadError(skillPath, fmt.Errorf("duplicate skill name %q", skill.Name))
		}

		skills[skill.Name] = skill
	}

	return NewSkillRegistry(skills), nil
}

// parseSkillFile reads and parses a single SKILL.md file.
// Returns nil, nil if the file doesn't exist.
func parseSkillFile(path string) (*SkillConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read file: %w", err)
	}

	content := string(data)

	fm, body, err := parseFrontmatter(content)
	if err != nil {
		return nil, err
	}

	if fm.Name == "" {
		return nil, fmt.Errorf("missing required field 'name' in frontmatter")
	}
	if fm.Description == "" {
		return nil, fmt.Errorf("missing required field 'description' in frontmatter")
	}

	return &SkillConfig{
		Name:        fm.Name,
		Description: fm.Description,
		Body:        body,
	}, nil
}

// parseFrontmatter splits SKILL.md content into frontmatter and body.
// Expected format: ---\n<yaml>\n---\n<markdown body>
func parseFrontmatter(content string) (skillFrontmatter, string, error) {
	var fm skillFrontmatter

	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	trimmed := strings.TrimLeftFunc(content, unicode.IsSpace)
	if !strings.HasPrefix(trimmed, "---") {
		return fm, "", fmt.Errorf("missing frontmatter delimiters (expected '---' at start)")
	}

	// Find the closing --- delimiter (skip the opening one)
	rest := trimmed[3:]
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}

	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return fm, "", fmt.Errorf("missing closing frontmatter delimiter '---'")
	}

	fmContent := rest[:endIdx]
	body := rest[endIdx+4:] // skip "\n---"
	// Strip at most one leading newline (the line break ending the --- line)
	// but preserve all other whitespace to keep markdown formatting intact.
	body, _ = strings.CutPrefix(body, "\n")

	if err := yaml.Unmarshal([]byte(fmContent), &fm); err != nil {
		return fm, "", fmt.Errorf("invalid frontmatter YAML: %w", err)
	}

	return fm, body, nil
}
