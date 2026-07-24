package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	echo "github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
)

func TestUsageSummaryHandler_Validation(t *testing.T) {
	s := &Server{}
	validWindow := "start_date=2024-01-01T00:00:00Z&end_date=2024-02-01T00:00:00Z"

	tests := []struct {
		name    string
		query   string
		wantErr int
		errMsg  string
	}{
		{
			name:    "missing start_date",
			query:   "end_date=2024-02-01T00:00:00Z",
			wantErr: http.StatusBadRequest,
			errMsg:  "start_date is required",
		},
		{
			name:    "missing end_date",
			query:   "start_date=2024-01-01T00:00:00Z",
			wantErr: http.StatusBadRequest,
			errMsg:  "end_date is required",
		},
		{
			name:    "invalid start_date",
			query:   "start_date=not-a-date&end_date=2024-02-01T00:00:00Z",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid start_date",
		},
		{
			name:    "end_date wrong format",
			query:   "start_date=2024-01-01T00:00:00Z&end_date=2024-01-01",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid end_date",
		},
		{
			name:    "start_date equal to end_date",
			query:   "start_date=2024-01-01T00:00:00Z&end_date=2024-01-01T00:00:00Z",
			wantErr: http.StatusBadRequest,
			errMsg:  "start_date must be before end_date",
		},
		{
			name:    "start_date after end_date",
			query:   "start_date=2024-02-01T00:00:00Z&end_date=2024-01-01T00:00:00Z",
			wantErr: http.StatusBadRequest,
			errMsg:  "start_date must be before end_date",
		},
		{
			name:    "invalid rank_by",
			query:   validWindow + "&rank_by=sessions",
			wantErr: http.StatusBadRequest,
			errMsg:  "invalid rank_by",
		},
		{
			name:    "window longer than 365 days",
			query:   "start_date=2024-01-01T00:00:00Z&end_date=2025-01-02T00:00:00Z",
			wantErr: http.StatusBadRequest,
			errMsg:  "date window must not exceed 365 days",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/summary?"+tt.query, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := s.usageSummaryHandler(c)
			if assert.Error(t, err) {
				he, ok := err.(*echo.HTTPError)
				if assert.True(t, ok, "expected echo.HTTPError") {
					assert.Equal(t, tt.wantErr, he.Code)
					assert.Contains(t, he.Message, tt.errMsg)
				}
			}
		})
	}

	t.Run("rank_by=cost rejected when estimation disabled", func(t *testing.T) {
		svc := newUsageTestSessionService(&ent.Client{})
		svc.SetCostEstimationEnabled(false)
		disabled := &Server{sessionService: svc}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/summary?"+validWindow+"&rank_by=cost", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := disabled.usageSummaryHandler(c)
		if assert.Error(t, err) {
			he, ok := err.(*echo.HTTPError)
			if assert.True(t, ok, "expected echo.HTTPError") {
				assert.Equal(t, http.StatusBadRequest, he.Code)
				assert.Contains(t, he.Message, "rank_by=cost requires cost estimation")
			}
		}
	})

	t.Run("valid rank_by values pass validation", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		server := &Server{sessionService: newUsageTestSessionService(client.Client)}

		for _, v := range []string{"", "cost", "tokens"} {
			t.Run("rank_by="+v, func(t *testing.T) {
				query := validWindow
				if v != "" {
					query += "&rank_by=" + v
				}
				e := echo.New()
				req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/summary?"+query, nil)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)

				err := server.usageSummaryHandler(c)
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, rec.Code)

				var resp models.UsageSummaryResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				if v == "" {
					assert.Equal(t, models.UsageRankByCost, resp.RankBy)
				} else {
					assert.Equal(t, models.UsageRankBy(v), resp.RankBy)
				}
			})
		}
	})
}

func newUsageTestSessionService(client *ent.Client) *services.SessionService {
	chainRegistry := config.NewChainRegistry(map[string]*config.ChainConfig{
		"k8s-analysis": {
			AlertTypes: []string{"kubernetes"},
			Stages: []config.StageConfig{
				{
					Name: "analysis",
					Agents: []config.StageAgentConfig{
						{Name: config.AgentNameKubernetes},
					},
				},
			},
		},
	})
	mcpServerRegistry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"kubernetes-server": {
			Transport: config.TransportConfig{
				Type:    config.TransportTypeStdio,
				Command: "test-command",
			},
		},
	})
	return services.NewSessionService(client, chainRegistry, mcpServerRegistry)
}
