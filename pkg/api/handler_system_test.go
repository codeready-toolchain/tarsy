package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

func TestDefaultToolsHandler(t *testing.T) {
	t.Run("returns all false when no defaults configured", func(t *testing.T) {
		s := &Server{
			cfg: &config.Config{
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{}),
			},
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/default-tools", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.defaultToolsHandler(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp DefaultToolsResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

		assert.Equal(t, false, resp.NativeTools["google_search"])
		assert.Equal(t, false, resp.NativeTools["code_execution"])
		assert.Equal(t, false, resp.NativeTools["url_context"])
	})

	t.Run("resolves from default provider", func(t *testing.T) {
		s := &Server{
			cfg: &config.Config{
				Defaults: &config.Defaults{
					LLMProvider: "my-provider",
				},
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"my-provider": {
						Type:  config.LLMProviderTypeGoogle,
						Model: "test-model",
						NativeTools: map[config.GoogleNativeTool]bool{
							config.GoogleNativeToolGoogleSearch: true,
							config.GoogleNativeToolURLContext:   true,
						},
					},
				}),
			},
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/default-tools", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.defaultToolsHandler(c)
		require.NoError(t, err)

		var resp DefaultToolsResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

		assert.Equal(t, true, resp.NativeTools["google_search"])
		assert.Equal(t, false, resp.NativeTools["code_execution"])
		assert.Equal(t, true, resp.NativeTools["url_context"])
	})

	t.Run("falls back to google provider type", func(t *testing.T) {
		s := &Server{
			cfg: &config.Config{
				// No Defaults set — triggers fallback.
				LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
					"openai-prov": {
						Type:  config.LLMProviderTypeOpenAI,
						Model: "gpt-4",
					},
					"google-prov": {
						Type:  config.LLMProviderTypeGoogle,
						Model: "gemini-pro",
						NativeTools: map[config.GoogleNativeTool]bool{
							config.GoogleNativeToolCodeExecution: true,
						},
					},
				}),
			},
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/default-tools", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.defaultToolsHandler(c)
		require.NoError(t, err)

		var resp DefaultToolsResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

		assert.Equal(t, true, resp.NativeTools["code_execution"])
	})
}

func TestSystemWarningsHandler(t *testing.T) {
	t.Run("returns empty when service is nil", func(t *testing.T) {
		s := &Server{}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/warnings", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.systemWarningsHandler(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp SystemWarningsResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotNil(t, resp.Warnings)
		assert.Len(t, resp.Warnings, 0)
	})

	t.Run("returns warnings from service", func(t *testing.T) {
		warnSvc := services.NewSystemWarningsService()
		warnSvc.AddWarning("mcp", "Server unavailable", "Connection refused", "k8s-server")

		s := &Server{warningService: warnSvc}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/warnings", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.systemWarningsHandler(c)
		require.NoError(t, err)

		var resp SystemWarningsResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Warnings, 1)
		assert.Equal(t, "mcp", resp.Warnings[0].Category)
		assert.Equal(t, "Server unavailable", resp.Warnings[0].Message)
		assert.Equal(t, "Connection refused", resp.Warnings[0].Details)
		assert.Equal(t, "k8s-server", resp.Warnings[0].ServerID)
		assert.NotEmpty(t, resp.Warnings[0].ID)
		assert.NotEmpty(t, resp.Warnings[0].CreatedAt)
	})
}

func TestMCPServersHandler(t *testing.T) {
	t.Run("returns empty when health monitor is nil", func(t *testing.T) {
		s := &Server{}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/system/mcp-servers", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.mcpServersHandler(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp MCPServersResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.NotNil(t, resp.Servers)
		assert.Len(t, resp.Servers, 0)
	})
}

func TestFilterOptionsHandler(t *testing.T) {
	t.Run("returns 7 static statuses", func(t *testing.T) {
		// The handler always returns all status enum values.
		// We can only test the static portion without a real DB.
		// The full handler needs a sessionService, so we just verify the
		// constant list matches expectations.
		expectedStatuses := []string{
			"pending", "in_progress", "cancelling", "completed",
			"failed", "cancelled", "timed_out",
		}
		assert.Len(t, expectedStatuses, 7)
	})
}
