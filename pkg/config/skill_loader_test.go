package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSkills(t *testing.T) {
	t.Run("loads multiple valid skills", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "k8s", "---\nname: kubernetes-basics\ndescription: Kubernetes troubleshooting\n---\n# Kubernetes Basics\n\nCheck pod status first.")
		writeTestSkill(t, dir, "net", "---\nname: networking\ndescription: Network debugging skills\n---\n# Networking\n\nCheck DNS resolution.")

		registry, err := LoadSkills(dir)
		require.NoError(t, err)

		assert.Equal(t, 2, registry.Len())

		k8s, err := registry.Get("kubernetes-basics")
		require.NoError(t, err)
		assert.Equal(t, "kubernetes-basics", k8s.Name)
		assert.Equal(t, "Kubernetes troubleshooting", k8s.Description)
		assert.Equal(t, "# Kubernetes Basics\n\nCheck pod status first.", k8s.Body)

		net, err := registry.Get("networking")
		require.NoError(t, err)
		assert.Equal(t, "networking", net.Name)
		assert.Equal(t, "Network debugging skills", net.Description)
		assert.Equal(t, "# Networking\n\nCheck DNS resolution.", net.Body)
	})

	t.Run("missing skills directory returns empty registry", func(t *testing.T) {
		missingDir := filepath.Join(t.TempDir(), "missing")
		registry, err := LoadSkills(missingDir)
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

		var loadErr *LoadError
		require.ErrorAs(t, err, &loadErr)
		assert.Equal(t, "missing frontmatter delimiters (expected '---' at start)", loadErr.Err.Error())
	})

	t.Run("missing name field returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "no-name", "---\ndescription: has description\n---\n# Body")

		_, err := LoadSkills(dir)

		var loadErr *LoadError
		require.ErrorAs(t, err, &loadErr)
		assert.Equal(t, "missing required field 'name' in frontmatter", loadErr.Err.Error())
	})

	t.Run("missing description field returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "no-desc", "---\nname: no-desc\n---\n# Body")

		_, err := LoadSkills(dir)

		var loadErr *LoadError
		require.ErrorAs(t, err, &loadErr)
		assert.Equal(t, "missing required field 'description' in frontmatter", loadErr.Err.Error())
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

		var loadErr *LoadError
		require.ErrorAs(t, err, &loadErr)
		assert.Equal(t, `duplicate skill name "duplicate-name"`, loadErr.Err.Error())
	})

	t.Run("invalid YAML in frontmatter returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "bad-yaml", "---\n{{{\n---\n# Body")

		_, err := LoadSkills(dir)

		var loadErr *LoadError
		require.ErrorAs(t, err, &loadErr)
		assert.Contains(t, loadErr.Err.Error(), "invalid frontmatter YAML")
	})

	t.Run("missing closing frontmatter delimiter returns error", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "no-close", "---\nname: test\ndescription: test\n# Body without closing ---")

		_, err := LoadSkills(dir)

		var loadErr *LoadError
		require.ErrorAs(t, err, &loadErr)
		assert.Equal(t, "missing closing frontmatter delimiter '---'", loadErr.Err.Error())
	})

	t.Run("errors are wrapped in LoadError with file path", func(t *testing.T) {
		dir := t.TempDir()
		writeTestSkill(t, dir, "bad", "---\nname: bad\n---\n# Missing description")

		_, err := LoadSkills(dir)

		var loadErr *LoadError
		require.ErrorAs(t, err, &loadErr)
		assert.Equal(t, filepath.Join(dir, "skills", "bad", "SKILL.md"), loadErr.File)
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
		name       string
		content    string
		wantName   string
		wantDesc   string
		wantBody   string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:     "standard skill file",
			content:  "---\nname: my-skill\ndescription: A useful skill\n---\n\n# My Skill\n\nSome content here.",
			wantName: "my-skill",
			wantDesc: "A useful skill",
			wantBody: "\n# My Skill\n\nSome content here.",
		},
		{
			name:     "body preserves whitespace",
			content:  "---\nname: test\ndescription: test\n---\n\n  \n# Body\n\n  ",
			wantName: "test",
			wantDesc: "test",
			wantBody: "\n  \n# Body\n\n  ",
		},
		{
			name:     "body immediately after closing delimiter",
			content:  "---\nname: test\ndescription: test\n---\n# Body",
			wantName: "test",
			wantDesc: "test",
			wantBody: "# Body",
		},
		{
			name:       "no frontmatter delimiters",
			content:    "# Just markdown",
			wantErr:    true,
			wantErrMsg: "missing frontmatter delimiters (expected '---' at start)",
		},
		{
			name:       "only opening delimiter",
			content:    "---\nname: test\ndescription: test",
			wantErr:    true,
			wantErrMsg: "missing closing frontmatter delimiter '---'",
		},
		{
			name:     "CRLF line endings",
			content:  "---\r\nname: crlf-skill\r\ndescription: Windows file\r\n---\r\n\r\n# CRLF Body\r\n",
			wantName: "crlf-skill",
			wantDesc: "Windows file",
			wantBody: "\n# CRLF Body\n",
		},
		{
			name:     "lone CR line endings",
			content:  "---\rname: cr-skill\rdescription: Old Mac file\r---\r\r# CR Body\r",
			wantName: "cr-skill",
			wantDesc: "Old Mac file",
			wantBody: "\n# CR Body\n",
		},
		{
			name:       "malformed closing delimiter with extra dashes",
			content:    "---\nname: test\ndescription: test\n----\n# Body",
			wantErr:    true,
			wantErrMsg: "missing closing frontmatter delimiter '---'",
		},
		{
			name:       "malformed closing delimiter with trailing text",
			content:    "---\nname: test\ndescription: test\n---extra\n# Body",
			wantErr:    true,
			wantErrMsg: "missing closing frontmatter delimiter '---'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := parseFrontmatter(tt.content)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Equal(t, tt.wantErrMsg, err.Error())
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
