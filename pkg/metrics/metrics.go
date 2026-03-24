// Package metrics provides Prometheus metrics for TARSy.
package metrics

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Histogram bucket definitions tuned for each metric domain.
var (
	LLMBuckets     = []float64{1, 2, 5, 10, 20, 30, 60, 90, 120, 180}
	MCPBuckets     = []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60}
	SessionBuckets = []float64{30, 60, 120, 180, 300, 600, 900, 1200, 1800}
)

// Session lifecycle metrics.
var (
	SessionsSubmittedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_sessions_submitted_total",
		Help: "Alerts submitted via API.",
	}, []string{"alert_type"})

	SessionsTerminalTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_sessions_terminal_total",
		Help: "Sessions reaching terminal state.",
	}, []string{"alert_type", "status"})

	SessionDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tarsy_session_duration_seconds",
		Help:    "Processing time from claim to completion.",
		Buckets: SessionBuckets,
	}, []string{"alert_type", "status"})

	SessionWaitSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tarsy_session_wait_seconds",
		Help:    "Queue wait time from submit to claim.",
		Buckets: SessionBuckets,
	}, []string{"alert_type"})

	SessionsActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "tarsy_sessions_active",
		Help: "In-progress sessions (DB-polled).",
	})

	SessionsQueued = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "tarsy_sessions_queued",
		Help: "Pending sessions (DB-polled).",
	})
)

// Worker pool metrics.
var (
	WorkersTotal = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "tarsy_workers_total",
		Help: "Configured worker count (set once at startup).",
	})

	WorkersActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "tarsy_workers_active",
		Help: "Workers currently processing a session.",
	})

	OrphansRecoveredTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "tarsy_orphans_recovered_total",
		Help: "Orphaned sessions recovered.",
	})
)

// LLM call metrics.
var (
	LLMCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_llm_calls_total",
		Help: "LLM Generate calls.",
	}, []string{"provider", "model"})

	LLMErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_llm_errors_total",
		Help: "LLM call failures.",
	}, []string{"provider", "model", "error_code"})

	LLMDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tarsy_llm_duration_seconds",
		Help:    "LLM call wall-clock time.",
		Buckets: LLMBuckets,
	}, []string{"provider", "model"})

	LLMTokensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_llm_tokens_total",
		Help: "Tokens consumed (input/output/thinking).",
	}, []string{"provider", "model", "direction"})

	LLMFallbacksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_llm_fallbacks_total",
		Help: "Provider fallback switches.",
	}, []string{"from_provider", "to_provider"})
)

// MCP tool call metrics.
var (
	MCPCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_mcp_calls_total",
		Help: "MCP tool calls made.",
	}, []string{"server", "tool"})

	MCPErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_mcp_errors_total",
		Help: "MCP tool call failures.",
	}, []string{"server", "tool"})

	MCPDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tarsy_mcp_duration_seconds",
		Help:    "MCP tool call latency.",
		Buckets: MCPBuckets,
	}, []string{"server", "tool"})

	MCPHealthStatus = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tarsy_mcp_health_status",
		Help: "MCP server health probe result (1=healthy, 0=unhealthy).",
	}, []string{"server"})
)

// Review quality metrics.
var (
	SessionsReviewedTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "tarsy_sessions_reviewed_total",
		Help: "Sessions by quality rating (DB-polled).",
	}, []string{"quality_rating"})

	ReviewsCompletedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_reviews_completed_total",
		Help: "Reviews completed (event-driven).",
	}, []string{"quality_rating"})
)

// Action stage outcome metrics.
var ActionStageOutcomesTotal = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "tarsy_action_stage_outcomes_total",
	Help: "Action stages by agent name and whether actions were executed (DB-polled).",
}, []string{"agent_name", "actions_executed"})

// HTTP API metrics.
var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "tarsy_http_requests_total",
		Help: "HTTP requests handled.",
	}, []string{"method", "path", "status_code"})

	HTTPDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tarsy_http_duration_seconds",
		Help:    "HTTP request latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})
)

// WSConnectionsActive tracks active WebSocket connections.
var WSConnectionsActive = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "tarsy_ws_connections_active",
	Help: "Active WebSocket connections.",
})

// LLMTokens carries token counts without importing pkg/agent.
type LLMTokens struct {
	Input, Output, Thinking int
}

// ObserveLLMCall records metrics for a single LLM call. Safe to call with
// nil tokens.
func ObserveLLMCall(provider, model string, duration time.Duration, tokens *LLMTokens, err error) {
	LLMCallsTotal.WithLabelValues(provider, model).Inc()
	LLMDurationSeconds.WithLabelValues(provider, model).Observe(duration.Seconds())

	if err != nil {
		LLMErrorsTotal.WithLabelValues(provider, model, errorCode(err)).Inc()
	}

	if tokens != nil {
		LLMTokensTotal.WithLabelValues(provider, model, "input").Add(float64(tokens.Input))
		LLMTokensTotal.WithLabelValues(provider, model, "output").Add(float64(tokens.Output))
		if tokens.Thinking > 0 {
			LLMTokensTotal.WithLabelValues(provider, model, "thinking").Add(float64(tokens.Thinking))
		}
	}
}

// errorCode extracts a short, bounded classification from an error.
func errorCode(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}
