package config

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// LabelMapBuiltin is the reserved catalog key for the default exclusive
// watch/action/noise map. Empty label_map selectors resolve to this key.
const LabelMapBuiltin = "builtin"

var labelTokenRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

var labelMapAllowedKeys = map[string]bool{
	"multi":        true,
	"instructions": true,
	"labels":       true,
}

var labelSpecAllowedKeys = map[string]bool{
	"label":       true,
	"description": true,
}

var labelSpecKeyHints = map[string]string{
	"name": "label",
}

// LabelMap is one named entry in the label_maps catalog.
// Labels is a sequence so YAML/prompt order is preserved.
type LabelMap struct {
	Multi        bool        `yaml:"multi"`
	Instructions string      `yaml:"instructions,omitempty"`
	Labels       []LabelSpec `yaml:"labels"`
}

// LabelSpec is one closed-list tag in a LabelMap.
type LabelSpec struct {
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
}

// UnmarshalYAML rejects unknown keys (e.g. name instead of label).
func (m *LabelMap) UnmarshalYAML(value *yaml.Node) error {
	type raw LabelMap
	return decodeMapping(value, labelMapAllowedKeys, nil, (*raw)(m))
}

// UnmarshalYAML rejects unknown keys and trims label/description.
func (s *LabelSpec) UnmarshalYAML(value *yaml.Node) error {
	type raw LabelSpec
	if err := decodeMapping(value, labelSpecAllowedKeys, labelSpecKeyHints, (*raw)(s)); err != nil {
		return err
	}
	s.Label = strings.TrimSpace(s.Label)
	s.Description = strings.TrimSpace(s.Description)
	return nil
}

// ValidLabelToken reports whether s is an ASCII catalog key or label token:
// letter, then letters, digits, underscore, or hyphen.
func ValidLabelToken(s string) bool {
	return labelTokenRe.MatchString(s)
}

const builtinLabelMapInstructions = `This map encodes what to do with the alert, not whether automated tools already ran (actions_executed) and not review_status.

Apply at most one of watch, action, or noise. If the report disagrees with itself, prefer watch over action. Prefer watch over noise when unsure. Prefer noise over omitting the LABELS line when the classification is clearly closable with no follow-up.

There is no none or ignore label. An omitted LABELS line is not a close verdict: operators must still read the summary before closing when no label applies.`

var builtinLabelSpecs = []LabelSpec{
	{
		Label: "watch",
		Description: `Use when: the subject still looks off or evidence is thin. No intervention now; look again if it persists.

Do not use when: the analysis already concluded the alert can be closed with no follow-up (noise). Detector-tuning as the reason to keep the alert open is not watch.`,
	},
	{
		Label: "action",
		Description: `Use when: a human must intervene on the affected system (fix the failing workload, approve an emergency change, execute a runbook step that was not automated).

Do not use when: closing or acking the alert; filing a backlog ticket; tuning detection/rules; "no remediation because it was benign." Not the same as actions_executed. Closable-with-no-work is noise, not action.`,
	},
	{
		Label: "noise",
		Description: `Use when: this alert can be closed with no remaining human work. False positive, expected/maintenance, already recovered and done, duplicate of known-benign. Optional "tune later" in passing does not block noise.

Do not use when: a human should still look again (watch), intervene (action), or do a required follow-up (file a ticket, change a rule) before the alert is done. Unsure → watch, never noise. Omitting the LABELS line is not noise.`,
	},
}

// BuiltinLabelMap returns the Go default exclusive map (watch / action / noise).
func BuiltinLabelMap() LabelMap {
	return LabelMap{
		Multi:        false,
		Instructions: builtinLabelMapInstructions,
		Labels:       slices.Clone(builtinLabelSpecs),
	}
}

// injectBuiltinLabelMap clones catalog and ensures the builtin key exists.
// yamlOverride is true when YAML already defined label_maps.builtin (full replace, no merge).
func injectBuiltinLabelMap(catalog map[string]LabelMap) (map[string]LabelMap, bool) {
	yamlOverride := false
	if catalog == nil {
		catalog = make(map[string]LabelMap)
	} else {
		catalog = maps.Clone(catalog)
		_, yamlOverride = catalog[LabelMapBuiltin]
	}
	if !yamlOverride {
		catalog[LabelMapBuiltin] = BuiltinLabelMap()
	}
	return catalog, yamlOverride
}

// ResolveLabelMap returns the catalog entry for the last non-empty selector.
// Empty / omitted layers inherit the previous layer; all empty → builtin.
func ResolveLabelMap(catalog map[string]LabelMap, layers ...string) (LabelMap, error) {
	name := LabelMapBuiltin
	for _, layer := range layers {
		if layer != "" {
			name = layer
		}
	}
	m, ok := catalog[name]
	if !ok {
		return LabelMap{}, fmt.Errorf("unknown label map %q", name)
	}
	m.Labels = slices.Clone(m.Labels)
	return m, nil
}
