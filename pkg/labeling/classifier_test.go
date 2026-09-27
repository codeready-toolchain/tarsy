package labeling

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpaneas/jev"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

func TestClassifier_Classify(t *testing.T) {
	t.Parallel()
	custom := config.LabelMap{
		Instructions: "Page only when intervention is needed.",
		Labels:       []config.LabelSpec{{Label: "page", Description: "Human intervention required."}},
	}
	for _, tc := range []struct {
		name       string
		labelMap   config.LabelMap
		choice     string
		confidence float64
		wantLabels []string
		accepted   bool
	}{
		{"label", config.BuiltinLabelMap(), "action", 0.95, []string{"action"}, true},
		{"threshold equality", config.BuiltinLabelMap(), "watch", 0.9, []string{"watch"}, true},
		{"uncertain", config.BuiltinLabelMap(), "watch", 0.89, nil, false},
		{"explicit none", config.BuiltinLabelMap(), noLabel, 0.95, []string{}, true},
		{"uncertain none", config.BuiltinLabelMap(), noLabel, 0.7, nil, false},
		{"custom map", custom, "page", 0.99, []string{"page"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requests := make(chan json.RawMessage, 1)
			client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/v1/systemone", r.URL.Path)
				var request json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				requests <- request
				w.Header().Set("X-Request-ID", "test-request")
				assert.NoError(t, json.NewEncoder(w).Encode(choiceResponse(tc.labelMap, tc.choice, tc.confidence)))
			})
			analysis := "The analysis says intervention is required. <evidence>"
			result, metadata, err := New(client, 0.9).Classify(t.Context(), analysis, tc.labelMap)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tc.wantLabels, result.Labels)
			assert.Equal(t, tc.accepted, result.Accepted)
			assert.Equal(t, tc.confidence, result.Confidence)
			if tc.choice == noLabel {
				assert.Empty(t, result.Choice)
			} else {
				assert.Equal(t, tc.choice, result.Choice)
			}
			assert.Len(t, result.Probabilities, len(tc.labelMap.Labels)+1)
			assert.Equal(t, 1.0, result.Probabilities[tc.choice])
			assert.Equal(t, jev.Metadata{Model: "jev-test", RequestID: "test-request", Usage: jev.Usage{InputTokens: 120, OutputTokens: 20}}, metadata)

			var sent map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(<-requests, &sent))
			delete(sent, "model")
			sentJSON, err := json.Marshal(sent)
			require.NoError(t, err)
			auditJSON, err := json.Marshal(result.Request)
			require.NoError(t, err)
			assert.JSONEq(t, string(sentJSON), string(auditJSON), "audit must match the submitted state and question")
			var state map[string]string
			require.NoError(t, json.Unmarshal(sent["state"], &state))
			assert.Equal(t, map[string]string{"final_analysis": analysis}, state, "no executive summary or existing labels")
			questions := result.Request["questions"].(map[string]any)
			question := questions[questionID].(map[string]any)
			assert.Equal(t, tc.labelMap.Instructions, question["instructions"].(map[string]string)["label_map_instructions"])
			for _, spec := range tc.labelMap.Labels {
				assert.Equal(t, spec.Description, question["criteria"].(map[string]string)[spec.Label])
			}
			serialized, err := json.Marshal(result)
			require.NoError(t, err)
			var persisted Result
			require.NoError(t, json.Unmarshal(serialized, &persisted))
			assert.Equal(t, tc.wantLabels, persisted.Labels, "JSON must preserve none versus uncertainty")
			assert.Equal(t, result.Probabilities, persisted.Probabilities)
		})
	}
}

func TestClassifier_RejectsBeforeRequest(t *testing.T) {
	t.Parallel()
	tooMany := config.LabelMap{}
	for i := range 255 {
		tooMany.Labels = append(tooMany.Labels, config.LabelSpec{Label: fmt.Sprintf("label%d", i), Description: "A label."})
	}
	largeInstructions := config.BuiltinLabelMap()
	largeInstructions.Instructions = strings.Repeat("x", maxInputBytes)
	for _, tc := range []struct {
		name       string
		analysis   string
		labelMap   config.LabelMap
		confidence float64
		wantErr    error
	}{
		{"multi", "analysis", config.LabelMap{Multi: true}, 0.9, ErrUnsupportedMap},
		{"empty map", "analysis", config.LabelMap{}, 0.9, ErrUnsupportedMap},
		{"too many labels", "analysis", tooMany, 0.9, ErrUnsupportedMap},
		{"reserved sentinel", "analysis", config.LabelMap{Labels: []config.LabelSpec{{Label: noLabel, Description: "Collision"}}}, 0.9, ErrUnsupportedMap},
		{"duplicate", "analysis", config.LabelMap{Labels: []config.LabelSpec{{Label: "page", Description: "Page"}, {Label: "Page", Description: "Page again"}}}, 0.9, ErrUnsupportedMap},
		{"empty description", "analysis", config.LabelMap{Labels: []config.LabelSpec{{Label: "page"}}}, 0.9, ErrUnsupportedMap},
		{"empty analysis", " \n", config.BuiltinLabelMap(), 0.9, jev.ErrInvalidInput},
		{"large analysis", strings.Repeat("x", maxInputBytes), config.BuiltinLabelMap(), 0.9, ErrInputTooLarge},
		{"escaped bytes", strings.Repeat("<", 5000), config.BuiltinLabelMap(), 0.9, ErrInputTooLarge},
		{"large instructions", "analysis", largeInstructions, 0.9, ErrInputTooLarge},
		{"invalid threshold", "analysis", config.BuiltinLabelMap(), math.NaN(), jev.ErrInvalidInput},
		{"infinite threshold", "analysis", config.BuiltinLabelMap(), math.Inf(1), jev.ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := fixtureClient(t, func(_ http.ResponseWriter, _ *http.Request) { calls.Add(1) })
			result, metadata, err := New(client, tc.confidence).Classify(t.Context(), tc.analysis, tc.labelMap)
			require.ErrorIs(t, err, tc.wantErr)
			assert.Nil(t, result)
			assert.Zero(t, metadata)
			assert.Zero(t, calls.Load())
		})
	}
}

func TestClassifier_EvaluationErrorsPreserveAudit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		status     int
		malformed  bool
		wantTokens int
	}{
		{"HTTP failure", http.StatusServiceUnavailable, false, 0},
		{"invalid choice", http.StatusOK, true, 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := fixtureClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Request-ID", "failed-request")
				w.WriteHeader(tc.status)
				response := choiceResponse(config.BuiltinLabelMap(), "not_offered", 0.95)
				assert.NoError(t, json.NewEncoder(w).Encode(response))
			})
			result, metadata, err := New(client, 0.9).Classify(t.Context(), "analysis", config.BuiltinLabelMap())
			require.Error(t, err)
			require.NotNil(t, result)
			assert.NotEmpty(t, result.Request)
			assert.Nil(t, result.Labels)
			assert.False(t, result.Accepted)
			assert.Equal(t, tc.wantTokens, metadata.Usage.InputTokens)
			if tc.malformed {
				assert.ErrorIs(t, err, jev.ErrInvalidResponse)
				assert.Equal(t, "failed-request", metadata.RequestID)
			} else {
				var apiErr *jev.APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, tc.status, apiErr.StatusCode)
				assert.Equal(t, "failed-request", apiErr.RequestID)
			}
		})
	}
}

func TestClassifier_ContextCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	client := fixtureClient(t, func(_ http.ResponseWriter, _ *http.Request) {
		cancel(context.Canceled)
	})
	result, _, err := New(client, 0.9).Classify(ctx, "analysis", config.BuiltinLabelMap())
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	assert.False(t, result.Accepted)
	assert.NotEmpty(t, result.Request)

	expired, deadlineCancel := context.WithDeadlineCause(t.Context(), time.Now().Add(-time.Second), context.DeadlineExceeded)
	defer deadlineCancel()
	_, _, err = New(client, 0.9).Classify(expired, "analysis", config.BuiltinLabelMap())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func fixtureClient(t *testing.T, handler http.HandlerFunc) *jev.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := jev.New("test-key", jev.WithBaseURL(server.URL), jev.WithMaxRetries(0))
	require.NoError(t, err)
	return client
}

func choiceResponse(labelMap config.LabelMap, choice string, confidence float64) map[string]any {
	probabilities := map[string]float64{noLabel: 0}
	for _, label := range labelMap.Labels {
		probabilities[label.Label] = 0
	}
	probabilities[choice] = 1
	return map[string]any{
		"model": "jev-test", "usage": map[string]int{"input_tokens": 120, "output_tokens": 20},
		"answers": map[string]any{questionID: map[string]any{
			"type": "choice", "choice": choice, "confidence": confidence, "probabilities": probabilities,
		}},
	}
}
