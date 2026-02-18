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
	"github.com/codeready-toolchain/tarsy/pkg/runbook"
)

func TestHandleListRunbooks(t *testing.T) {
	t.Run("nil runbook service returns empty array", func(t *testing.T) {
		s := &Server{
			cfg:            &config.Config{},
			runbookService: nil,
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runbooks", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.handleListRunbooks(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var result []string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		assert.Equal(t, []string{}, result)
	})

	t.Run("service with no repo URL returns empty array", func(t *testing.T) {
		svc := runbook.NewService(nil, "", "default")
		s := &Server{
			cfg:            &config.Config{},
			runbookService: svc,
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runbooks", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.handleListRunbooks(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var result []string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		assert.Equal(t, []string{}, result)
	})

	t.Run("service returns runbook list from mock server", func(t *testing.T) {
		// Set up mock GitHub API server
		items := `[
			{"name":"k8s.md","path":"runbooks/k8s.md","type":"file","html_url":"https://github.com/org/repo/blob/main/runbooks/k8s.md"},
			{"name":"net.md","path":"runbooks/net.md","type":"file","html_url":"https://github.com/org/repo/blob/main/runbooks/net.md"}
		]`
		mockGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(items))
		}))
		defer mockGH.Close()

		rbCfg := &config.RunbookConfig{
			RepoURL:  "https://github.com/org/repo/tree/main/runbooks",
			CacheTTL: 1 * time.Minute,
		}
		svc := runbook.NewService(rbCfg, "", "default")
		// Inject test transport into the service's GitHub client
		svc.OverrideHTTPClientForTest(&http.Client{
			Transport: &redirectTransport{target: mockGH.URL},
		})

		s := &Server{
			cfg:            &config.Config{},
			runbookService: svc,
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/runbooks", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.handleListRunbooks(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var result []string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		assert.Len(t, result, 2)
	})
}

// redirectTransport redirects all requests to the test server URL.
type redirectTransport struct {
	target string
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = t.target[7:] // Strip "http://"
	return http.DefaultTransport.RoundTrip(req)
}
