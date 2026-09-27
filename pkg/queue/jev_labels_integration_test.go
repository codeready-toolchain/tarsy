package queue

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/labeling"
	util "github.com/codeready-toolchain/tarsy/test/util"
	"github.com/drpaneas/jev"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func jevLabelsTestConfig() *config.Config {
	cfg := testConfig("test-chain", &config.ChainConfig{AlertTypes: []string{"test-alert"}})
	cfg.ExperimentalJEVLabels = &config.ExperimentalJEVLabelsConfig{
		Enabled: true, Model: "jev-test", Timeout: time.Second, MinConfidence: 0.9,
	}
	return cfg
}

func jevLabelsTestExecutor(t *testing.T, client *ent.Client, cfg *config.Config, handler http.HandlerFunc) (*RealSessionExecutor, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	jevClient, err := jev.New("test-key", jev.WithBaseURL(server.URL), jev.WithHTTPClient(server.Client()), jev.WithModel("jev-test"), jev.WithMaxRetries(0))
	require.NoError(t, err)
	executor := NewRealSessionExecutor(cfg, client, nil, nil, nil, nil, nil, nil)
	executor.SetLabelClassifier(labeling.New(jevClient, 0.9))
	return executor, &calls
}

func jevLabelsCompletedSession(t *testing.T, client *ent.Client, labels []string) *ent.AlertSession {
	t.Helper()
	session := createExecutorTestSession(t, client, "test-chain")
	update := client.AlertSession.UpdateOneID(session.ID).
		SetStatus(alertsession.StatusCompleted).
		SetFinalAnalysis("The workload has recovered. No human intervention remains.").
		SetExecutiveSummary("Production summary must remain unchanged.").
		SetReviewStatus(alertsession.ReviewStatusNeedsReview).
		SetCompletedAt(time.Now())
	if labels != nil {
		update.SetLabels(labels)
	}
	session, err := update.Save(t.Context())
	require.NoError(t, err)
	// Compare persisted timestamps, including PostgreSQL's timestamp precision.
	session, err = client.AlertSession.Get(t.Context(), session.ID)
	require.NoError(t, err)
	return session
}

func assertJEVLabelsSessionUnchanged(t *testing.T, client *ent.Client, before *ent.AlertSession) {
	t.Helper()
	after, err := client.AlertSession.Get(t.Context(), before.ID)
	require.NoError(t, err)
	assert.Equal(t, before.Status, after.Status)
	assert.Equal(t, before.Labels, after.Labels)
	assert.Equal(t, before.FinalAnalysis, after.FinalAnalysis)
	assert.Equal(t, before.ExecutiveSummary, after.ExecutiveSummary)
	assert.Equal(t, before.ExecutiveSummaryError, after.ExecutiveSummaryError)
	assert.Equal(t, before.ReviewStatus, after.ReviewStatus)
	assert.Equal(t, before.CompletedAt, after.CompletedAt)
	assert.Equal(t, before.ErrorMessage, after.ErrorMessage)
}

func TestJEVLabelsShadowComparison(t *testing.T) {
	client, _ := util.SetupTestDatabase(t)
	tests := []struct {
		name       string
		production []string
		choice     string
		confidence float64
		comparison string
	}{
		{name: "agreement", production: []string{"noise"}, choice: "noise", confidence: 0.95, comparison: "agree"},
		{name: "disagreement", production: []string{"action"}, choice: "noise", confidence: 0.95, comparison: "disagree"},
		{name: "uncertain", production: []string{"noise"}, choice: "noise", confidence: 0.7, comparison: "uncertain"},
		{name: "unavailable baseline", choice: "noise", confidence: 0.95, comparison: "baseline_unavailable"},
		{name: "valid empty baseline", production: []string{}, choice: "__no_label__", confidence: 0.95, comparison: "agree"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := jevLabelsTestConfig()
			requests := make(chan map[string]any, 1)
			executor, calls := jevLabelsTestExecutor(t, client, cfg, func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				requests <- request
				probabilities := map[string]float64{"watch": 1 - tt.confidence, "action": 0, "noise": 0, "__no_label__": 0}
				probabilities[tt.choice] = tt.confidence
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{
					"model": "jev-test", "usage": map[string]int{"input_tokens": 100, "output_tokens": 20},
					"answers": map[string]any{"label": map[string]any{
						"type": "choice", "choice": tt.choice, "confidence": tt.confidence, "probabilities": probabilities,
					}},
				}); err != nil {
					t.Errorf("write response: %v", err)
				}
			})
			session := jevLabelsCompletedSession(t, client, tt.production)
			executor.EvaluateSessionLabels(t.Context(), session.ID)
			require.EqualValues(t, 1, calls.Load())
			request := <-requests
			encodedRequest, err := json.Marshal(request)
			require.NoError(t, err)
			assert.Contains(t, string(encodedRequest), *session.FinalAnalysis)
			assert.NotContains(t, string(encodedRequest), *session.ExecutiveSummary)
			assert.NotContains(t, string(encodedRequest), "production_labels")

			interaction, err := client.LLMInteraction.Query().Where(llminteraction.SessionIDEQ(session.ID)).Only(t.Context())
			require.NoError(t, err)
			assert.Equal(t, llminteraction.InteractionTypeExecutiveSummary, interaction.InteractionType)
			assert.Equal(t, "jev-test", interaction.ModelName)
			assert.Nil(t, interaction.StageID)
			assert.Nil(t, interaction.ExecutionID)
			assert.Nil(t, interaction.LastMessageID)
			assert.Nil(t, interaction.ErrorMessage)
			assert.Equal(t, "jev_shadow_labels", interaction.ResponseMetadata["kind"])
			assert.Equal(t, "shadow", interaction.ResponseMetadata["mode"])
			assert.Equal(t, tt.comparison, interaction.ResponseMetadata["comparison"])
			assert.Equal(t, tt.production != nil, interaction.ResponseMetadata["production_labels_available"])
			wantProduction, err := json.Marshal(tt.production)
			require.NoError(t, err)
			gotProduction, err := json.Marshal(interaction.ResponseMetadata["production_labels"])
			require.NoError(t, err)
			assert.JSONEq(t, string(wantProduction), string(gotProduction))
			assert.Equal(t, tt.confidence >= 0.9, interaction.LlmResponse["accepted"])
			assert.Equal(t, tt.confidence, interaction.LlmResponse["confidence"])
			wantChoice := tt.choice
			if wantChoice == "__no_label__" {
				wantChoice = ""
			}
			assert.Equal(t, wantChoice, interaction.LlmResponse["choice"])
			var wantLabels []string
			if tt.confidence >= 0.9 {
				wantLabels = []string{}
				if wantChoice != "" {
					wantLabels = append(wantLabels, wantChoice)
				}
			}
			wantLabelsJSON, err := json.Marshal(wantLabels)
			require.NoError(t, err)
			gotLabelsJSON, err := json.Marshal(interaction.LlmResponse["labels"])
			require.NoError(t, err)
			assert.JSONEq(t, string(wantLabelsJSON), string(gotLabelsJSON))
			probabilities, ok := interaction.LlmResponse["probabilities"].(map[string]any)
			require.True(t, ok)
			assert.Len(t, probabilities, 4)
			assert.Equal(t, tt.confidence, probabilities[tt.choice])
			require.NotNil(t, interaction.InputTokens)
			require.NotNil(t, interaction.OutputTokens)
			assert.Equal(t, 100, *interaction.InputTokens)
			assert.Equal(t, 20, *interaction.OutputTokens)
			assert.NotNil(t, interaction.DurationMs)
			assertJEVLabelsSessionUnchanged(t, client, session)
		})
	}
}

func TestJEVLabelsShadowDisabledAndUnsupported(t *testing.T) {
	client, _ := util.SetupTestDatabase(t)
	for _, name := range []string{"no configuration", "disabled", "no classifier", "multi label map", "failed session"} {
		t.Run(name, func(t *testing.T) {
			cfg := jevLabelsTestConfig()
			executor, calls := jevLabelsTestExecutor(t, client, cfg, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			})
			session := jevLabelsCompletedSession(t, client, []string{"watch"})
			switch name {
			case "no configuration":
				cfg.ExperimentalJEVLabels = nil
			case "disabled":
				cfg.ExperimentalJEVLabels.Enabled = false
			case "no classifier":
				executor.SetLabelClassifier(nil)
			case "multi label map":
				labelMap := config.BuiltinLabelMap()
				labelMap.Multi = true
				cfg.LabelMaps[config.LabelMapBuiltin] = labelMap
			case "failed session":
				var err error
				session, err = client.AlertSession.UpdateOneID(session.ID).SetStatus(alertsession.StatusFailed).Save(t.Context())
				require.NoError(t, err)
			}
			executor.EvaluateSessionLabels(t.Context(), session.ID)
			assert.Zero(t, calls.Load())
			count, err := client.LLMInteraction.Query().Where(llminteraction.SessionIDEQ(session.ID)).Count(t.Context())
			require.NoError(t, err)
			assert.Zero(t, count, "skipped evaluation should not create an LLM interaction")
			assertJEVLabelsSessionUnchanged(t, client, session)
		})
	}
}

func TestJEVLabelsShadowFailurePreservesCompletedSession(t *testing.T) {
	client, _ := util.SetupTestDatabase(t)
	for _, name := range []string{"HTTP failure", "invalid response", "timeout"} {
		t.Run(name, func(t *testing.T) {
			cfg := jevLabelsTestConfig()
			if name == "timeout" {
				cfg.ExperimentalJEVLabels.Timeout = 250 * time.Millisecond
			}
			executor, calls := jevLabelsTestExecutor(t, client, cfg, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Request-ID", "failed-request")
				if name == "timeout" {
					select {
					case <-r.Context().Done():
					case <-time.After(5 * time.Second):
					}
					return
				}
				if name == "invalid response" {
					_, _ = w.Write([]byte(`{"answers":`))
					return
				}
				http.Error(w, "upstream unavailable", http.StatusBadGateway)
			})
			session := jevLabelsCompletedSession(t, client, []string{"watch"})
			start := time.Now()
			executor.EvaluateSessionLabels(t.Context(), session.ID)
			assert.Less(t, time.Since(start), 2*time.Second)
			require.EqualValues(t, 1, calls.Load())
			interaction, err := client.LLMInteraction.Query().Where(llminteraction.SessionIDEQ(session.ID)).Only(t.Context())
			require.NoError(t, err)
			require.NotNil(t, interaction.ErrorMessage)
			assert.NotEmpty(t, *interaction.ErrorMessage)
			assert.Nil(t, interaction.InputTokens, "failed requests must not invent usage")
			assert.Nil(t, interaction.OutputTokens)
			assert.Equal(t, "jev_shadow_labels", interaction.ResponseMetadata["kind"])
			if name != "timeout" {
				assert.Equal(t, "failed-request", interaction.ResponseMetadata["request_id"])
			}
			assertJEVLabelsSessionUnchanged(t, client, session)
		})
	}
}

type jevLabelsWorkerExecutor struct {
	execute  func(context.Context, *ent.AlertSession) *ExecutionResult
	evaluate func(context.Context, string)
}

func (e *jevLabelsWorkerExecutor) Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult {
	return e.execute(ctx, session)
}

func (e *jevLabelsWorkerExecutor) EvaluateSessionLabels(ctx context.Context, sessionID string) {
	e.evaluate(ctx, sessionID)
}

func TestJEVLabelsWorkerRunsOnlyAfterSuccessfulCompletion(t *testing.T) {
	for _, status := range []alertsession.Status{alertsession.StatusCompleted, alertsession.StatusFailed, alertsession.StatusCancelled, alertsession.StatusTimedOut} {
		t.Run(string(status), func(t *testing.T) {
			client, _ := util.SetupTestDatabase(t)
			session := createTestSession(t.Context(), t, client)
			pub := &mockEventPublisher{}
			var evaluations int
			executor := &jevLabelsWorkerExecutor{
				execute: func(context.Context, *ent.AlertSession) *ExecutionResult {
					return &ExecutionResult{Status: status, FinalAnalysis: "Final report", ExecutiveSummary: "Summary"}
				},
				evaluate: func(ctx context.Context, id string) {
					evaluations++
					assert.Equal(t, session.ID, id)
					_, hasDeadline := ctx.Deadline()
					assert.False(t, hasDeadline, "shadow hook must receive worker context, not session deadline")
					stored, err := client.AlertSession.Get(ctx, id)
					require.NoError(t, err)
					assert.Equal(t, alertsession.StatusCompleted, stored.Status)
					assert.NotNil(t, stored.CompletedAt)
					assert.Equal(t, new(alertsession.ReviewStatusNeedsReview), stored.ReviewStatus)
					require.NotNil(t, pub.lastSessionStatus)
					assert.Equal(t, alertsession.StatusCompleted, pub.lastSessionStatus.Status)
					assert.Equal(t, 1, pub.reviewStatusCount, "terminal notifications precede shadow evaluation")
				},
			}
			cfg := intTestQueueConfig()
			pool := NewWorkerPool("test-pod", client, cfg, executor, nil, pub, nil)
			worker := NewWorker("test-worker", "test-pod", client, cfg, executor, nil, pool, pub, nil)
			require.NoError(t, worker.pollAndProcess(t.Context()))
			if status == alertsession.StatusCompleted {
				assert.Equal(t, 1, evaluations)
			} else {
				assert.Zero(t, evaluations)
			}
		})
	}
}

func TestJEVLabelsWorkerSkipsWhenTerminalWriteLoses(t *testing.T) {
	client, _ := util.SetupTestDatabase(t)
	createTestSession(t.Context(), t, client)
	var evaluations int
	executor := &jevLabelsWorkerExecutor{
		execute: func(ctx context.Context, session *ent.AlertSession) *ExecutionResult {
			// Simulate another owner finalizing before this worker's CAS.
			_, err := client.AlertSession.UpdateOneID(session.ID).SetStatus(alertsession.StatusCompleted).SetCompletedAt(time.Now()).Save(ctx)
			require.NoError(t, err)
			return &ExecutionResult{Status: alertsession.StatusCompleted}
		},
		evaluate: func(context.Context, string) { evaluations++ },
	}
	cfg := intTestQueueConfig()
	pool := NewWorkerPool("test-pod", client, cfg, executor, nil, nil, nil)
	worker := NewWorker("test-worker", "test-pod", client, cfg, executor, nil, pool, nil, nil)
	require.NoError(t, worker.pollAndProcess(t.Context()))
	assert.Zero(t, evaluations)
}
