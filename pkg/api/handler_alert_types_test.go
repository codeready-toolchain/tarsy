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
)

func TestAlertTypesHandler(t *testing.T) {
	t.Run("returns sorted alert types with default chain", func(t *testing.T) {
		s := &Server{
			cfg: &config.Config{
				ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
					"z-chain": {
						AlertTypes:  []string{"alert-z"},
						Description: "Z chain",
					},
					"a-chain": {
						AlertTypes:  []string{"alert-a1", "alert-a2"},
						Description: "A chain",
					},
				}),
			},
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/alert-types", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.alertTypesHandler(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp AlertTypesResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

		// Default chain should be the first sorted: "a-chain".
		assert.Equal(t, "a-chain", resp.DefaultChainID)

		// Alert types should follow sorted chain order: a-chain's types first, then z-chain.
		require.Len(t, resp.AlertTypes, 3)
		assert.Equal(t, "alert-a1", resp.AlertTypes[0].Type)
		assert.Equal(t, "a-chain", resp.AlertTypes[0].ChainID)
		assert.Equal(t, "A chain", resp.AlertTypes[0].Description)
		assert.Equal(t, "alert-a2", resp.AlertTypes[1].Type)
		assert.Equal(t, "alert-z", resp.AlertTypes[2].Type)
		assert.Equal(t, "z-chain", resp.AlertTypes[2].ChainID)
	})

	t.Run("returns empty array for no chains", func(t *testing.T) {
		s := &Server{
			cfg: &config.Config{
				ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{}),
			},
		}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/alert-types", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := s.alertTypesHandler(c)
		require.NoError(t, err)

		var resp AlertTypesResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

		assert.Empty(t, resp.DefaultChainID)
		assert.NotNil(t, resp.AlertTypes, "should be empty array, not nil")
		assert.Len(t, resp.AlertTypes, 0)
	})
}
