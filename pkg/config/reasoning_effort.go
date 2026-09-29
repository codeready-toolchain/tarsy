package config

import (
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const reasoningEffortWarnMsg = "reasoning_effort is outside the documented set"

var wellKnownReasoningEfforts = []string{
	ReasoningEffortLow,
	ReasoningEffortMedium,
	ReasoningEffortHigh,
	ReasoningEffortXHigh,
	ReasoningEffortMax,
}

type modelFamily int

const (
	familyNone modelFamily = iota
	familyOpenAI
	familyXAI
	familyGemini
	familyClaude
)

type modelClass struct {
	family       modelFamily
	recognized   bool
	eligible     bool
	major        int
	minor        int
	claudeFamily string
}

var (
	gptModelRE      = regexp.MustCompile(`(?i)^gpt-(\d+)(?:\.(\d+))?`)
	grokModelRE     = regexp.MustCompile(`(?i)^grok-(\d+)(?:\.(\d+))?`)
	geminiModelRE   = regexp.MustCompile(`(?i)^gemini-(\d+)(?:\.(\d+))?`)
	claudeModelRE   = regexp.MustCompile(`(?i)^claude-([a-z]+)-(.+)$`)
	claudeDateRE    = regexp.MustCompile(`-\d{8}$`)
	claudeVersionRE = regexp.MustCompile(`^(\d+)(?:[.-](\d+))?$`)
)

// EffectiveReasoningEffort is the value sent on LLMConfig and shown in the config viewer.
// A non-empty configured value is returned unchanged. An omitted value is "high" when the
// model is eligible, and empty otherwise.
func EffectiveReasoningEffort(model, configured string) string {
	if configured != "" {
		return configured
	}
	if classifyModel(model).eligible {
		return ReasoningEffortHigh
	}
	return ""
}

// EffectiveReasoningEffort is the effort sent for this provider.
// A nil provider returns empty.
func (p *LLMProviderConfig) EffectiveReasoningEffort() string {
	if p == nil {
		return ""
	}
	return EffectiveReasoningEffort(p.Model, p.ReasoningEffort)
}

func checkReasoningEffort(name string, p *LLMProviderConfig) error {
	effort := p.ReasoningEffort
	if effort == "" {
		return nil
	}
	if strings.TrimSpace(effort) == "" {
		return NewValidationError("llm_provider", name, "reasoning_effort",
			fmt.Errorf("must not be empty or whitespace"))
	}
	warnReasoningEffort(name, p)
	return nil
}

func warnReasoningEffort(name string, p *LLMProviderConfig) {
	effort := p.ReasoningEffort
	wellKnown := slices.Contains(wellKnownReasoningEfforts, effort)
	class := classifyModel(p.Model)
	checkDocumented := p.BaseURL == "" && class.recognized
	documented := documentedReasoningEfforts(class)
	outsideDocumented := checkDocumented && !slices.Contains(documented, effort)
	if wellKnown && !outsideDocumented {
		return
	}

	levels := strings.Join(wellKnownReasoningEfforts, ", ")
	if checkDocumented {
		if len(documented) == 0 {
			levels = "none"
		} else {
			levels = strings.Join(documented, ", ")
		}
	}
	slog.Warn(reasoningEffortWarnMsg,
		"provider", name,
		"model", p.Model,
		"reasoning_effort", effort,
		"documented_levels", levels,
	)
}

func documentedReasoningEfforts(class modelClass) []string {
	if !class.recognized || !class.eligible {
		return nil
	}
	switch class.family {
	case familyOpenAI:
		return []string{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh, ReasoningEffortMax}
	case familyXAI:
		return []string{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh}
	case familyGemini:
		return []string{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}
	case familyClaude:
		if class.claudeFamily == "opus" || class.major >= 5 {
			return []string{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh, ReasoningEffortMax}
		}
		return []string{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortMax}
	default:
		return nil
	}
}

func classifyModel(model string) modelClass {
	if m := gptModelRE.FindStringSubmatch(model); m != nil {
		major, minor, ok := versionFromMatch(m)
		if !ok {
			return modelClass{}
		}
		eligible := versionGTE(major, minor, 5, 6) &&
			!modelContains(model, "-chat") &&
			!modelContains(model, "-main")
		return modelClass{
			family:     familyOpenAI,
			recognized: true,
			eligible:   eligible,
			major:      major,
			minor:      minor,
		}
	}
	if m := grokModelRE.FindStringSubmatch(model); m != nil {
		major, minor, ok := versionFromMatch(m)
		if !ok {
			return modelClass{}
		}
		return modelClass{
			family:     familyXAI,
			recognized: true,
			eligible:   versionGTE(major, minor, 4, 6),
			major:      major,
			minor:      minor,
		}
	}
	if m := geminiModelRE.FindStringSubmatch(model); m != nil {
		major, minor, ok := versionFromMatch(m)
		if !ok {
			return modelClass{}
		}
		eligible := versionGTE(major, minor, 3, 8) && !modelContains(model, "image")
		return modelClass{
			family:     familyGemini,
			recognized: true,
			eligible:   eligible,
			major:      major,
			minor:      minor,
		}
	}
	if m := claudeModelRE.FindStringSubmatch(model); m != nil {
		rest := claudeDateRE.ReplaceAllString(m[2], "")
		vm := claudeVersionRE.FindStringSubmatch(rest)
		if vm == nil {
			return modelClass{}
		}
		major, minor, ok := versionFromMatch(vm)
		if !ok {
			return modelClass{}
		}
		return modelClass{
			family:       familyClaude,
			recognized:   true,
			eligible:     versionGTE(major, minor, 4, 8),
			major:        major,
			minor:        minor,
			claudeFamily: strings.ToLower(m[1]),
		}
	}
	return modelClass{}
}

func versionFromMatch(m []string) (major, minor int, ok bool) {
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, false
	}
	if m[2] == "" {
		return major, 0, true
	}
	minor, err = strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

func versionGTE(major, minor, wantMajor, wantMinor int) bool {
	if major != wantMajor {
		return major > wantMajor
	}
	return minor >= wantMinor
}

func modelContains(model, sub string) bool {
	return strings.Contains(strings.ToLower(model), sub)
}
