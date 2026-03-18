package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSkills(t *testing.T) {
	t.Run("valid skills from testdata", func(t *testing.T) {
		registry, err := LoadSkills("testdata")
		require.NoError(t, err)

		assert.Equal(t, 2, registry.Len())

		k8s, err := registry.Get("kubernetes-basics")
		require.NoError(t, err)
		assert.Equal(t, "kubernetes-basics", k8s.Name)
		assert.Contains(t, k8s.Description, "Kubernetes")
		assert.Contains(t, k8s.Body, "# Kubernetes Basics")

		net, err := registry.Get("networking")
		require.NoError(t, err)
		assert.Equal(t, "networking", net.Name)
		assert.Contains(t, net.Description, "Network")
	})

	t.Run("missing skills directory returns empty registry", func(t *testing.T) {
		registry, err := LoadSkills("/nonexistent/path")
		require.NoError(t, err)
		assert.Equal(t, 0, registry.Len())
	})

	t.Run("empty skills directory returns empty registry", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "skills"), 0o755))

		registry, err := LoadSkills(dir)
		require.NoError(t, err)
		assert.Equal(t, 0, registry.Len())
	})

	t.Run("directory without SKILL.md is skipped", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "skills", "empty-dir"), 0o755))

		registry, err := LoadSkills(dir)
		require.NoError(t, err)
		assert.Equal(t, 0, registry.Len())
	})

	t.Run("non-directory entries in skills/ are skipped", func(t *testing.T) {
		dir := t.TempDir()
		skillsDir := filepath.Join(dir, "skills")
		require.NoError(t, os.MkdirAll(skillsDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(skillsDir, "not-a-dir.txt"), []byte("hello"), 0o644))

		registry, err := LoadSkills(dir)
		require.NoError(t, err)
		assert.Equal(t, 0, registry.Len())
	})

	t.Run("missing frontmatter returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "bad-skill", "# No frontmatter here\nJust markdown.")

		_, err := LoadSkills(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "frontmatter")
	})

	t.Run("missing name field returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "no-name", "---\ndescription: has description\n---\n# Body")

		_, err := LoadSkills(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})

	t.Run("missing description field returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "no-desc", "---\nname: no-desc\n---\n# Body")

		_, err := LoadSkills(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "description")
	})

	t.Run("empty body is valid", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "empty-body", "---\nname: empty-body\ndescription: a skill with no body\n---\n")

		registry, err := LoadSkills(dir)
		require.NoError(t, err)

		skill, err := registry.Get("empty-body")
		require.NoError(t, err)
		assert.Equal(t, "", skill.Body)
	})

	t.Run("duplicate skill names across directories returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "dir-a", "---\nname: duplicate-name\ndescription: first\n---\n# A")
		writeTestSkill(t, dir, "dir-b", "---\nname: duplicate-name\ndescription: second\n---\n# B")

		_, err := LoadSkills(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate skill name")
	})

	t.Run("invalid YAML in frontmatter returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "bad-yaml", "---\n{{{\n---\n# Body")

		_, err := LoadSkills(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "YAML")
	})

	t.Run("missing closing frontmatter delimiter returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "no-close", "---\nname: test\ndescription: test\n# Body without closing ---")

		_, err := LoadSkills(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "closing frontmatter delimiter")
	})

	t.Run("errors are wrapped in LoadError", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "bad", "---\nname: bad\n---\n# Missing description")

		_, err := LoadSkills(dir)
		require.Error(t, err)

		var loadErr *LoadError
		require.ErrorAs(t, err, &loadErr)
		assert.Contains(t, loadErr.File, "bad")
		assert.Contains(t, loadErr.File, "SKILL.md")
	})

	t.Run("skill name comes from frontmatter not directory name", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "directory-name", "---\nname: frontmatter-name\ndescription: Test skill\n---\n# Body")

		registry, err := LoadSkills(dir)
		require.NoError(t, err)

		assert.True(t, registry.Has("frontmatter-name"))
		assert.False(t, registry.Has("directory-name"))
	})
}

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantName    string
		wantDesc    string
		wantBody    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "standard skill file",
			content:  "---\nname: my-skill\ndescription: A useful skill\n---\n\n# My Skill\n\nSome content here.",
			wantName: "my-skill",
			wantDesc: "A useful skill",
			wantBody: "# My Skill\n\nSome content here.",
		},
		{
			name:     "body with leading/trailing whitespace is trimmed",
			content:  "---\nname: test\ndescription: test\n---\n\n  \n# Body\n\n  ",
			wantName: "test",
			wantDesc: "test",
			wantBody: "# Body",
		},
		{
			name:        "no frontmatter delimiters",
			content:     "# Just markdown",
			wantErr:     true,
			errContains: "frontmatter",
		},
		{
			name:        "only opening delimiter",
			content:     "---\nname: test\ndescription: test",
			wantErr:     true,
			errContains: "closing",
		},
		{
			name:     "CRLF line endings",
			content:  "---\r\nname: crlf-skill\r\ndescription: Windows file\r\n---\r\n\r\n# CRLF Body\r\n",
			wantName: "crlf-skill",
			wantDesc: "Windows file",
			wantBody: "# CRLF Body",
		},
		{
			name:     "lone CR line endings",
			content:  "---\rname: cr-skill\rdescription: Old Mac file\r---\r\r# CR Body\r",
			wantName: "cr-skill",
			wantDesc: "Old Mac file",
			wantBody: "# CR Body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := parseFrontmatter(tt.content)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantName, fm.Name)
			assert.Equal(t, tt.wantDesc, fm.Description)
			assert.Equal(t, tt.wantBody, body)
		})
	}
}

// writeTestSkill creates a SKILL.md file at dir/skills/name/SKILL.md.
func writeTestSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, "skills", name)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))
}
