package controller

import (
	"regexp"
	"slices"
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// labelsPrefixRe matches LABELS then optional whitespace then : then optional
// whitespace (case-insensitive). LABEL: without S does not match.
var labelsPrefixRe = regexp.MustCompile(`(?i)^LABELS\s*:\s*`)

// LabelsExtraction is the three-state result of ExtractLabels.
type LabelsExtraction struct {
	// Labels is the canonical unique names in map order. Empty when none apply
	// or the trailer is a valid empty remainder. Never nil when Valid is true.
	Labels []string
	// Cleaned is the response with a valid LABELS trailer stripped. When
	// HasTrailer is false or Valid is false, Cleaned is the trim-right original.
	Cleaned string
	// HasTrailer is true when the last non-empty line matched the LABELS: prefix.
	HasTrailer bool
	// Valid is true when there is no LABELS: trailer, or the trailer is well-formed
	// (including empty remainder).
	Valid bool
}

// ExtractLabels parses a LABELS: trailer from the last non-empty line of text.
func ExtractLabels(text string, m config.LabelMap) LabelsExtraction {
	trimmed := strings.TrimRight(text, "\n\r \t")
	empty := []string{}
	if trimmed == "" {
		return LabelsExtraction{Labels: empty, Cleaned: trimmed, Valid: true}
	}

	lastNewline := strings.LastIndex(trimmed, "\n")
	var lastLine, body string
	if lastNewline == -1 {
		lastLine = trimmed
	} else {
		lastLine = trimmed[lastNewline+1:]
		body = trimmed[:lastNewline]
	}

	line := trimWrappingMarkup(lastLine)
	loc := labelsPrefixRe.FindStringIndex(line)
	if loc == nil {
		return LabelsExtraction{Labels: empty, Cleaned: trimmed, Valid: true}
	}

	remainder := line[loc[1]:]
	invalid := LabelsExtraction{Cleaned: trimmed, HasTrailer: true, Valid: false}

	byLower := make(map[string]string, len(m.Labels))
	for _, spec := range m.Labels {
		byLower[strings.ToLower(spec.Label)] = spec.Label
	}

	seen := make(map[string]struct{})
	for token := range strings.SplitSeq(remainder, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		canonical, ok := matchLabelToken(token, byLower)
		if !ok {
			return invalid
		}
		seen[canonical] = struct{}{}
	}

	labels := make([]string, 0, len(seen))
	for _, spec := range m.Labels {
		if _, ok := seen[spec.Label]; ok {
			labels = append(labels, spec.Label)
		}
	}
	if !m.Multi && len(labels) > 1 {
		return invalid
	}

	return LabelsExtraction{
		Labels:     labels,
		Cleaned:    body,
		HasTrailer: true,
		Valid:      true,
	}
}

func matchLabelToken(token string, byLower map[string]string) (string, bool) {
	if c, ok := byLower[strings.ToLower(token)]; ok {
		return c, true
	}
	if token == "" {
		return "", false
	}
	last := token[len(token)-1]
	if last == '.' || last == ';' {
		if c, ok := byLower[strings.ToLower(token[:len(token)-1])]; ok {
			return c, true
		}
	}
	return "", false
}

func trimWrappingMarkup(line string) string {
	line = strings.TrimSpace(line)
	for len(line) >= 2 {
		first, last := line[0], line[len(line)-1]
		if (first == '*' || first == '_' || first == '`') && first == last {
			line = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		break
	}
	return line
}

// canonicalLabelsPtr returns a non-nil pointer to a cloned labels slice,
// using an empty non-nil slice when labels is nil.
func canonicalLabelsPtr(labels []string) *[]string {
	out := slices.Clone(labels)
	if out == nil {
		out = []string{}
	}
	return &out
}
