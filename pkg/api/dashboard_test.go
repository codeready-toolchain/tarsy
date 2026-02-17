package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDashboardTestServer creates a minimal Server with an Echo instance and
// dummy API + health routes, mimicking the real route registration order
// (API routes first, then dashboard routes via SetDashboardDir).
func newDashboardTestServer(t *testing.T) *Server {
	t.Helper()
	e := echo.New()
	s := &Server{echo: e}

	// Register API and health routes that should take priority over SPA fallback.
	e.GET("/health", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	e.GET("/api/v1/test", func(c *echo.Context) error {
		return c.String(http.StatusOK, "api-response")
	})
	return s
}

// writeDashboardFiles creates a temp directory with the given files and returns
// the directory path. Files are specified as relative path → content pairs.
func writeDashboardFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	return dir
}

func TestSetupDashboardRoutes(t *testing.T) {
	t.Run("no dashboard dir — no SPA fallback", func(t *testing.T) {
		s := newDashboardTestServer(t)
		// dashboardDir is empty — setupDashboardRoutes is a no-op.
		s.setupDashboardRoutes()

		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		// Root should not resolve to anything (404 or 405).
		assert.NotEqual(t, http.StatusOK, rec.Code)
	})

	t.Run("dashboard dir without index.html — skips", func(t *testing.T) {
		dir := t.TempDir() // empty directory
		s := newDashboardTestServer(t)
		s.dashboardDir = dir
		s.setupDashboardRoutes()

		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		assert.NotEqual(t, http.StatusOK, rec.Code)
	})

	t.Run("SPA fallback serves index.html for unknown paths", func(t *testing.T) {
		dir := writeDashboardFiles(t, map[string]string{
			"index.html": "<html><body>dashboard</body></html>",
		})
		s := newDashboardTestServer(t)
		s.dashboardDir = dir
		s.setupDashboardRoutes()

		tests := []struct {
			name string
			path string
		}{
			{name: "root", path: "/"},
			{name: "nested SPA route", path: "/sessions/abc"},
			{name: "deep SPA route", path: "/sessions/abc/trace"},
			{name: "submit page", path: "/submit-alert"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

				assert.Equal(t, http.StatusOK, rec.Code)
				assert.Contains(t, rec.Body.String(), "dashboard")
				assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"),
					"SPA fallback should set no-cache so browsers pick up new asset hashes after deployments")
			})
		}
	})

	t.Run("serves exact file when it exists on disk", func(t *testing.T) {
		dir := writeDashboardFiles(t, map[string]string{
			"index.html":  "<html>index</html>",
			"favicon.ico": "icon-data",
			"robots.txt":  "User-agent: *",
		})
		s := newDashboardTestServer(t)
		s.dashboardDir = dir
		s.setupDashboardRoutes()

		tests := []struct {
			name     string
			path     string
			contains string
		}{
			{name: "favicon", path: "/favicon.ico", contains: "icon-data"},
			{name: "robots.txt", path: "/robots.txt", contains: "User-agent"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

				assert.Equal(t, http.StatusOK, rec.Code)
				assert.Contains(t, rec.Body.String(), tt.contains)
				assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"),
					"unhashed root files should use no-cache")
			})
		}
	})

	t.Run("serves Vite assets from /assets/ with immutable cache", func(t *testing.T) {
		dir := writeDashboardFiles(t, map[string]string{
			"index.html":              "<html>index</html>",
			"assets/app-abc.js":       "console.log('app')",
			"assets/style-def123.css": "body { color: red }",
		})
		s := newDashboardTestServer(t)
		s.dashboardDir = dir
		s.setupDashboardRoutes()

		tests := []struct {
			name     string
			path     string
			contains string
		}{
			{name: "JS bundle", path: "/assets/app-abc.js", contains: "console.log"},
			{name: "CSS bundle", path: "/assets/style-def123.css", contains: "body"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

				assert.Equal(t, http.StatusOK, rec.Code)
				assert.Contains(t, rec.Body.String(), tt.contains)
				assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"),
					"hashed Vite assets should have aggressive cache headers")
			})
		}
	})

	t.Run("API routes take priority over SPA fallback", func(t *testing.T) {
		dir := writeDashboardFiles(t, map[string]string{
			"index.html": "<html>index</html>",
		})
		s := newDashboardTestServer(t)
		s.dashboardDir = dir
		s.setupDashboardRoutes()

		// Registered API route should still resolve normally.
		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "api-response", rec.Body.String())
	})

	t.Run("unregistered /api/ path returns 404 not index.html", func(t *testing.T) {
		dir := writeDashboardFiles(t, map[string]string{
			"index.html": "<html>index</html>",
		})
		s := newDashboardTestServer(t)
		s.dashboardDir = dir
		s.setupDashboardRoutes()

		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nonexistent", nil))

		// The SPA handler should NOT serve index.html for /api/* paths.
		assert.NotContains(t, rec.Body.String(), "index")
	})

	t.Run("/health route is not intercepted by SPA fallback", func(t *testing.T) {
		dir := writeDashboardFiles(t, map[string]string{
			"index.html": "<html>index</html>",
		})
		s := newDashboardTestServer(t)
		s.dashboardDir = dir
		s.setupDashboardRoutes()

		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "ok", rec.Body.String())
	})
}

func TestSetDashboardDir(t *testing.T) {
	t.Run("registers routes when called with valid dir", func(t *testing.T) {
		dir := writeDashboardFiles(t, map[string]string{
			"index.html": "<html>spa</html>",
		})
		s := newDashboardTestServer(t)

		// After SetDashboardDir, SPA fallback should work.
		s.SetDashboardDir(dir)

		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/some-page", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "spa")
	})

	t.Run("empty dir is a no-op", func(t *testing.T) {
		s := newDashboardTestServer(t)
		s.SetDashboardDir("")

		rec := httptest.NewRecorder()
		s.echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.NotEqual(t, http.StatusOK, rec.Code)
	})
}
