package queue

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/labeling"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	"github.com/drpaneas/jev"
)

// SetLabelClassifier enables the optional shadow experiment. Set before workers
// start; nil disables it without changing normal executive-summary labeling.
func (e *RealSessionExecutor) SetLabelClassifier(classifier *labeling.Classifier) {
	e.labelClassifier = classifier
}

// EvaluateSessionLabels records an experimental comparison, never a session
// update. The worker calls this only after publishing the completed session.
func (e *RealSessionExecutor) EvaluateSessionLabels(ctx context.Context, sessionID string) {
	cfg := e.cfg.ExperimentalJEVLabels
	if cfg == nil || !cfg.Enabled || e.labelClassifier == nil || ctx.Err() != nil {
		return
	}
	logger := slog.With("session_id", sessionID, "experiment", "jev_shadow_labels")
	evalCtx, cancel := context.WithTimeoutCause(ctx, cfg.Timeout, errors.New("experimental JEV labeling timed out"))
	defer cancel()

	session, err := e.dbClient.AlertSession.Get(evalCtx, sessionID)
	if err != nil {
		logger.Warn("Could not read session for JEV label experiment", "error", err)
		return
	}
	if session.Status != alertsession.StatusCompleted || session.DeletedAt != nil || session.FinalAnalysis == nil || *session.FinalAnalysis == "" {
		return
	}
	chain, err := e.cfg.GetChain(session.ChainID)
	if err != nil {
		logger.Warn("Could not resolve chain for JEV label experiment", "error", err)
		return
	}
	defaultMap := ""
	if e.cfg.Defaults != nil {
		defaultMap = e.cfg.Defaults.LabelMap
	}
	labelMap, err := config.ResolveLabelMap(e.cfg.LabelMaps, defaultMap, chain.LabelMap)
	if err != nil {
		logger.Warn("Could not resolve map for JEV label experiment", "error", err)
		return
	}

	started := time.Now()
	result, meta, evalErr := e.labelClassifier.Classify(evalCtx, *session.FinalAnalysis, labelMap)
	duration := time.Since(started)
	if result == nil {
		// Unsupported maps and oversized input are skipped locally, not billed.
		logger.Info("Skipped JEV label experiment", "reason", evalErr)
		return
	}
	if meta.RequestID == "" {
		if apiErr, ok := errors.AsType[*jev.APIError](evalErr); ok {
			meta.RequestID = apiErr.RequestID
		} else if responseErr, ok := errors.AsType[*jev.ResponseError](evalErr); ok {
			meta.RequestID = responseErr.RequestID
		}
	}
	request := result.Request
	request["model"] = cfg.Model
	response := map[string]any{}
	metadata := map[string]any{
		"kind": "jev_shadow_labels", "version": 1, "mode": "shadow",
		"production_labels": session.Labels, "production_labels_available": session.Labels != nil,
		"min_confidence": cfg.MinConfidence, "request_id": meta.RequestID,
		"usage_available": meta.Model != "",
	}
	model := cfg.Model
	if meta.Model != "" {
		model = meta.Model
	}
	req := models.CreateLLMInteractionRequest{
		SessionID: sessionID,
		// Reuse the session-level summary category; the experiment is identified
		// by metadata.kind, separate from all agent messages and timeline content.
		InteractionType: string(llminteraction.InteractionTypeExecutiveSummary),
		ModelName:       model, LLMRequest: request, LLMResponse: response,
		ResponseMetadata: metadata, DurationMs: new(int(duration.Milliseconds())),
	}
	var tokens *metrics.LLMTokens
	if meta.Model != "" {
		req.InputTokens = new(meta.Usage.InputTokens)
		req.OutputTokens = new(meta.Usage.OutputTokens)
		req.TotalTokens = new(meta.Usage.InputTokens + meta.Usage.OutputTokens)
		tokens = &metrics.LLMTokens{Input: meta.Usage.InputTokens, Output: meta.Usage.OutputTokens}
	}
	metrics.ObserveLLMCall("jev", model, duration, tokens, evalErr)
	if evalErr != nil {
		req.ErrorMessage = new(evalErr.Error())
		metadata["comparison"] = "error"
		logger.Warn("JEV label experiment failed", "error", evalErr)
	} else {
		response["choice"] = result.Choice
		response["labels"] = result.Labels
		response["accepted"] = result.Accepted
		response["confidence"] = result.Confidence
		response["probabilities"] = result.Probabilities
		switch {
		case !result.Accepted:
			metadata["comparison"] = "uncertain"
		case session.Labels == nil:
			metadata["comparison"] = "baseline_unavailable"
		case slices.Equal(session.Labels, result.Labels):
			metadata["comparison"] = "agree"
		default:
			metadata["comparison"] = "disagree"
		}
	}

	// The evaluation may have timed out. Give its audit write a separate bounded
	// budget, still owned by the worker lifecycle and cancelled on shutdown.
	writeCtx, writeCancel := context.WithTimeoutCause(ctx, 5*time.Second, errors.New("JEV label trace write timed out"))
	defer writeCancel()
	interactionService := services.NewInteractionService(e.dbClient, nil, e.costBook)
	interaction, err := interactionService.CreateLLMInteraction(writeCtx, req)
	if err != nil {
		logger.Warn("Could not record JEV label experiment", "error", err)
		return
	}
	if e.eventPublisher != nil {
		if err := e.eventPublisher.PublishInteractionCreated(writeCtx, sessionID, events.InteractionCreatedPayload{
			BasePayload: events.BasePayload{
				Type: events.EventTypeInteractionCreated, SessionID: sessionID,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			},
			InteractionID: interaction.ID, InteractionType: "llm",
		}); err != nil {
			logger.Warn("Could not publish JEV label trace event", "error", err)
		}
	}
}
