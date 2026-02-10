# Phase 4.2: Data Masking — Detailed Design

## Overview

Data masking prevents sensitive information (secrets, tokens, certificates, passwords) from reaching the LLM, logs, timeline events, and stored messages. Masking is applied at two chokepoints:

1. **MCP tool results** — after MCP call, before content enters the agent loop
2. **Alert payloads** — at API entry, before storage and processing

This phase implements the masking service in Go, integrating 15 built-in regex patterns (already defined in `pkg/config/builtin.go`), a code-based Kubernetes Secret masker, custom regex patterns from YAML config, and the two integration points above.

### Scope from Project Plan

- [x] Masking service (Go)
- [x] Regex-based maskers (15 patterns defined in builtin.go)
- [x] Custom data masking (regex) in configuration
- [x] MCP tool result masking integration
- [x] Alert payload sanitization
- [x] Distinguish K8s Secrets vs ConfigMaps

### What Already Exists

| Component | Location | Status |
|-----------|----------|--------|
| `MaskingConfig` / `MaskingPattern` types | `pkg/config/types.go` | Complete |
| 15 built-in regex patterns | `pkg/config/builtin.go` | Complete |
| 6 pattern groups (basic, secrets, security, kubernetes, cloud, all) | `pkg/config/builtin.go` | Complete |
| Code masker placeholder (`kubernetes_secret`) | `pkg/config/builtin.go` | Placeholder only |
| `MCPServerConfig.DataMasking` field | `pkg/config/mcp.go` | Complete |
| Config validation for masking | `pkg/config/validator.go` | Complete |
| Masking TODO stub in executor | `pkg/mcp/executor.go:100-102` | Stub comment |
| YAML config examples | `deploy/config/tarsy.yaml.example` | Complete |

---

## Architecture

### Design Principles

1. **Fail-closed for MCP** — on masking failure, redact the entire content rather than leaking secrets
2. **Fail-open for alerts** — on masking failure, continue with unmasked alert data (availability over secrecy for inbound data that the user already has)
3. **One-way masking** — original values are never stored or recoverable
4. **Code maskers before regex** — structural maskers (K8s Secrets) run first for precision, then regex patterns sweep remaining secrets
5. **Compile once** — all patterns compiled at service creation; config doesn't hot-reload
6. **Single chokepoint** — MCP content is masked in `ToolExecutor.Execute()` before `ToolResult` is returned, so all downstream consumers (timeline, messages, LLM) see masked content

### Package Layout

```
pkg/masking/
├── service.go                    # MaskingService — core orchestrator
├── pattern.go                    # CompiledPattern, pattern resolution, group expansion
├── masker.go                     # Masker interface for code-based maskers
├── kubernetes_secret.go          # KubernetesSecretMasker implementation
├── service_test.go               # MaskingService unit tests
├── pattern_test.go               # Pattern compilation and resolution tests
├── kubernetes_secret_test.go     # K8s Secret masker tests
└── testdata/                     # Test fixtures (YAML/JSON Secret/ConfigMap samples)
    ├── secret_yaml.txt
    ├── secret_json.txt
    ├── configmap_yaml.txt
    ├── secret_list_yaml.txt
    └── mixed_resources.txt
```

### Data Flow

```
MCP Tool Call Flow:
  MCP Server → Client.CallTool() → mcpsdk.CallToolResult
    → extractTextContent(result) → string content
    → MaskingService.MaskToolResult(content, serverID) ← NEW
    → agent.ToolResult{Content: masked}
    → Controller → Timeline/Messages/LLM (all see masked content)

Alert Submission Flow:
  POST /api/v1/alerts → handler → AlertService.SubmitAlert()
    → MaskingService.MaskAlertData(alertData, patternGroup) ← NEW
    → DB INSERT (masked alert_data stored)
    → Worker picks up session (already masked)
```

---

## Core Types & Interfaces

### Masker Interface (`pkg/masking/masker.go`)

```go
// Masker is the interface for code-based maskers that need structural awareness
// beyond regex pattern matching. Code-based maskers can parse YAML/JSON and
// apply context-sensitive masking (e.g., mask K8s Secrets but not ConfigMaps).
type Masker interface {
    // Name returns the unique identifier for this masker.
    // Must match the key in config.GetBuiltinConfig().CodeMaskers.
    Name() string

    // AppliesTo performs a lightweight check on whether this masker
    // should process the data. Should be fast (string contains, not parsing).
    AppliesTo(data string) bool

    // Mask applies masking logic and returns the masked result.
    // Must be defensive: return original data on parse/processing errors.
    Mask(data string) string
}
```

### CompiledPattern (`pkg/masking/pattern.go`)

```go
// CompiledPattern holds a pre-compiled regex pattern with its replacement.
type CompiledPattern struct {
    Name        string
    Regex       *regexp.Regexp
    Replacement string
    Description string
}
```

### MaskingService (`pkg/masking/service.go`)

```go
// MaskingService applies data masking to MCP tool results and alert payloads.
// Created once at application startup (singleton). Thread-safe and stateless
// aside from compiled patterns.
type MaskingService struct {
    registry        *config.MCPServerRegistry
    patterns        map[string]*CompiledPattern  // Built-in + custom compiled patterns
    patternGroups   map[string][]string          // Group name → pattern names
    codeMaskers     map[string]Masker            // Registered code-based maskers
    alertMasking    AlertMaskingConfig           // Alert payload masking settings
}
```

---

## MaskingService Implementation

### Constructor

```go
// NewMaskingService creates a masking service with compiled patterns and registered maskers.
// All patterns are compiled eagerly at creation time. Invalid patterns are logged and skipped.
func NewMaskingService(
    registry *config.MCPServerRegistry,
    alertCfg AlertMaskingConfig,
) *MaskingService {
    s := &MaskingService{
        registry:     registry,
        patterns:     make(map[string]*CompiledPattern),
        patternGroups: config.GetBuiltinConfig().PatternGroups,
        codeMaskers:  make(map[string]Masker),
        alertMasking: alertCfg,
    }

    // 1. Compile all built-in regex patterns
    s.compileBuiltinPatterns()

    // 2. Compile custom patterns from all MCP server configs
    s.compileCustomPatterns()

    // 3. Register code-based maskers
    s.registerMasker(&KubernetesSecretMasker{})

    return s
}
```

### MaskToolResult

Primary integration point for MCP tool results. Called from `ToolExecutor.Execute()`.

```go
// MaskToolResult applies server-specific masking to MCP tool result content.
// Returns masked content. On masking failure, returns a redaction notice (fail-closed).
func (s *MaskingService) MaskToolResult(content string, serverID string) string {
    if content == "" {
        return content
    }

    // 1. Look up server masking config
    serverCfg, err := s.registry.Get(serverID)
    if err != nil || serverCfg.DataMasking == nil || !serverCfg.DataMasking.Enabled {
        return content // No masking configured
    }

    // 2. Resolve patterns for this server
    patterns := s.resolvePatterns(serverCfg.DataMasking)
    if len(patterns) == 0 {
        return content
    }

    // 3. Apply masking with fail-closed error handling
    masked, err := s.applyMasking(content, patterns)
    if err != nil {
        slog.Error("Masking failed, redacting content (fail-closed)",
            "server", serverID, "error", err)
        return "[REDACTED: data masking failure — tool result could not be safely processed]"
    }

    return masked
}
```

### MaskAlertData

Integration point for alert payload sanitization.

```go
// MaskAlertData applies masking to alert payload data using the configured pattern group.
// Returns masked data. On masking failure, returns original data (fail-open for alerts).
func (s *MaskingService) MaskAlertData(data string) string {
    if !s.alertMasking.Enabled || data == "" {
        return data
    }

    patterns := s.resolvePatternsFromGroup(s.alertMasking.PatternGroup)
    if len(patterns) == 0 {
        return data
    }

    masked, err := s.applyMasking(data, patterns)
    if err != nil {
        slog.Error("Alert masking failed, continuing with unmasked data (fail-open)",
            "error", err)
        return data
    }

    return masked
}
```

### Pattern Resolution

```go
// resolvePatterns expands a MaskingConfig into a deduplicated list of CompiledPatterns
// and applicable code-based Maskers.
func (s *MaskingService) resolvePatterns(cfg *config.MaskingConfig) []*resolvedPatterns {
    // 1. Expand pattern_groups → individual pattern names
    // 2. Add individual patterns from cfg.Patterns
    // 3. Add custom_patterns (compiled at service creation, keyed as "custom_{name}")
    // 4. Deduplicate by name
    // 5. Separate into regex patterns and code masker names
}
```

### Core Masking Logic

```go
// applyMasking applies code-based maskers then regex patterns to content.
func (s *MaskingService) applyMasking(content string, resolved *resolvedPatterns) (string, error) {
    masked := content

    // Phase 1: Code-based maskers (more specific, structural awareness)
    for _, maskerName := range resolved.codeMaskerNames {
        masker, ok := s.codeMaskers[maskerName]
        if !ok {
            continue
        }
        if masker.AppliesTo(masked) {
            masked = masker.Mask(masked)
        }
    }

    // Phase 2: Regex patterns (general sweep)
    for _, pattern := range resolved.regexPatterns {
        masked = pattern.Regex.ReplaceAllString(masked, pattern.Replacement)
    }

    return masked, nil
}
```

### Internal Type: resolvedPatterns

```go
// resolvedPatterns holds the resolved set of maskers and patterns for a masking operation.
type resolvedPatterns struct {
    codeMaskerNames []string            // Names of code-based maskers to apply
    regexPatterns   []*CompiledPattern  // Compiled regex patterns to apply
}
```

---

## KubernetesSecretMasker

### Purpose

Distinguishes Kubernetes `Secret` resources from `ConfigMap` resources and masks only Secret `data`/`stringData` fields. This requires structural parsing — regex alone cannot reliably distinguish the two.

### Implementation (`pkg/masking/kubernetes_secret.go`)

```go
const MaskedSecretValue = "__MASKED_SECRET_DATA__"

type KubernetesSecretMasker struct{}

func (m *KubernetesSecretMasker) Name() string { return "kubernetes_secret" }

func (m *KubernetesSecretMasker) AppliesTo(data string) bool {
    // Fast string checks before regex
    if !strings.Contains(data, "Secret") {
        return false
    }
    // YAML: kind: Secret  |  JSON: "kind": "Secret" or "kind":"Secret"
    return yamlSecretPattern.MatchString(data) || jsonSecretPattern.MatchString(data)
}

func (m *KubernetesSecretMasker) Mask(data string) string {
    // 1. Try YAML format (most common kubectl output)
    if masked := m.maskYAML(data); masked != data {
        return masked
    }
    // 2. Try JSON format
    if masked := m.maskJSON(data); masked != data {
        return masked
    }
    return data
}
```

### YAML Masking Strategy

1. Parse with `gopkg.in/yaml.v3` using `yaml.Decoder` to handle multi-document YAML (`---` separators)
2. For each document, check if `kind: Secret`
3. If Secret: replace `data` and `stringData` map values with `__MASKED_SECRET_DATA__`
4. If ConfigMap or other kind: leave untouched
5. Re-serialize to YAML, preserving document boundaries
6. Also handle JSON-in-annotations (e.g., `kubectl.kubernetes.io/last-applied-configuration`)

### JSON Masking Strategy

1. Parse with `encoding/json`
2. Walk the object tree
3. For objects with `"kind": "Secret"`: replace `"data"` and `"stringData"` values with masked placeholder
4. Handle `items` arrays (for `kubectl get secrets -o json` list output)
5. Recurse into string values that may contain embedded JSON Secrets (annotations)

### Edge Cases

- Multi-document YAML with mixed Secrets and ConfigMaps
- Kubernetes List resources (`kind: SecretList` or `kind: List` with `items`)
- JSON embedded in YAML annotations (`last-applied-configuration`)
- Malformed YAML/JSON — return original data (defensive)
- Empty `data` fields — leave as-is (nothing to mask)

---

## Integration Points

### 1. ToolExecutor Integration (`pkg/mcp/executor.go`)

**Current state** (lines 100-102):
```go
// Steps 8-9: masking and summarization (stubs for Phase 4.1)
// TODO (Phase 4.2): content = e.maskingService.MaskResult(content, serverID)
// TODO (Phase 4.3): content = e.maybeSummarize(ctx, content, serverID, toolName)
```

**Changes:**

Add `maskingService` field to `ToolExecutor`:

```go
type ToolExecutor struct {
    client         *Client
    registry       *config.MCPServerRegistry
    serverIDs      []string
    toolFilter     map[string][]string
    maskingService *masking.MaskingService  // NEW
}
```

Update `NewToolExecutor` to accept the masking service:

```go
func NewToolExecutor(
    client *Client,
    registry *config.MCPServerRegistry,
    serverIDs []string,
    toolFilter map[string][]string,
    maskingService *masking.MaskingService,  // NEW
) *ToolExecutor
```

Replace the TODO stub in `Execute()`:

```go
// Step 7: Convert to ToolResult
content := extractTextContent(result)

// Step 8: Apply data masking (Phase 4.2)
if e.maskingService != nil {
    content = e.maskingService.MaskToolResult(content, serverID)
}

// Step 9: Summarization (Phase 4.3 stub)
// TODO (Phase 4.3): content = e.maybeSummarize(ctx, content, serverID, toolName)
```

### 2. ClientFactory Integration (`pkg/mcp/client_factory.go`)

Update `ClientFactory` to hold the masking service:

```go
type ClientFactory struct {
    registry       *config.MCPServerRegistry
    maskingService *masking.MaskingService  // NEW
}

func NewClientFactory(
    registry *config.MCPServerRegistry,
    maskingService *masking.MaskingService,  // NEW
) *ClientFactory
```

Wire through in `CreateToolExecutor`:

```go
func (f *ClientFactory) CreateToolExecutor(...) (*ToolExecutor, *Client, error) {
    client, err := f.CreateClient(ctx, serverIDs)
    if err != nil {
        return nil, nil, err
    }
    return NewToolExecutor(client, f.registry, serverIDs, toolFilter, f.maskingService), client, nil
}
```

### 3. Alert Submission Integration

**Option chosen**: Inject `MaskingService` into `AlertService`.

Update `AlertService`:

```go
type AlertService struct {
    client         *ent.Client
    chainRegistry  *config.ChainRegistry
    defaults       *config.Defaults
    maskingService *masking.MaskingService  // NEW (nullable for backward compat)
}
```

Update `SubmitAlert` to mask alert data before DB insert:

```go
func (s *AlertService) SubmitAlert(ctx context.Context, input SubmitAlertInput) (*ent.AlertSession, error) {
    // ... existing validation ...

    // Mask alert data before storage
    alertData := input.Data
    if s.maskingService != nil {
        alertData = s.maskingService.MaskAlertData(alertData)
    }

    // Create session with masked data
    builder := s.client.AlertSession.Create().
        SetAlertData(alertData).
        // ... rest unchanged
}
```

### 4. Application Startup Wiring

The `MaskingService` is created during application initialization and passed to both `ClientFactory` and `AlertService`:

```go
// In main.go or app setup
maskingService := masking.NewMaskingService(
    cfg.MCPServerRegistry,
    masking.AlertMaskingConfig{
        Enabled:      cfg.Defaults.AlertMasking.Enabled,
        PatternGroup: cfg.Defaults.AlertMasking.PatternGroup,
    },
)

clientFactory := mcp.NewClientFactory(cfg.MCPServerRegistry, maskingService)
alertService := services.NewAlertService(entClient, cfg.ChainRegistry, cfg.Defaults, maskingService)
```

---

## Configuration

### Alert Masking Configuration

Add to `Defaults` struct in `pkg/config/defaults.go`:

```go
type Defaults struct {
    // ... existing fields ...

    // Alert data masking configuration
    AlertMasking *AlertMaskingDefaults `yaml:"alert_masking,omitempty"`
}

type AlertMaskingDefaults struct {
    Enabled      bool   `yaml:"enabled"`       // Default: true
    PatternGroup string `yaml:"pattern_group"` // Default: "security"
}
```

**YAML example** (`tarsy.yaml`):

```yaml
defaults:
  alert_masking:
    enabled: true
    pattern_group: "security"  # basic, secrets, security, kubernetes, cloud, all
```

**Built-in default** (in `loader.go` defaults resolution):

```go
if defaults.AlertMasking == nil {
    defaults.AlertMasking = &AlertMaskingDefaults{
        Enabled:      true,
        PatternGroup: "security",
    }
}
```

### Per-Server MCP Masking (Existing)

Already fully configured via `MCPServerConfig.DataMasking`:

```yaml
mcp_servers:
  kubernetes-server:
    data_masking:
      enabled: true
      pattern_groups: ["kubernetes"]
      patterns: ["certificate", "token", "email"]
      custom_patterns:
        - pattern: "CUSTOM_SECRET_.*"
          replacement: "__MASKED_CUSTOM__"
          description: "Custom secret pattern"
```

### Config Validation Updates

Add to `pkg/config/validator.go`:

1. Validate `AlertMasking.PatternGroup` references a valid built-in group
2. Validate custom pattern regexes compile without error (no ReDoS check needed — Go's `regexp` uses RE2 which guarantees linear-time matching)

---

## Error Handling

### Fail-Closed (MCP Tool Results)

When masking fails during MCP tool result processing:

```go
// In MaskToolResult:
masked, err := s.applyMasking(content, patterns)
if err != nil {
    slog.Error("Masking failed, redacting content", "server", serverID, "error", err)
    return "[REDACTED: data masking failure — tool result could not be safely processed]"
}
```

The LLM receives a redaction notice instead of potentially sensitive data. The message explains *why* the result is missing but intentionally does NOT suggest retrying — the same content would likely trigger the same masking failure, risking a retry loop.

### Fail-Open (Alert Payloads)

When masking fails during alert payload processing:

```go
// In MaskAlertData:
masked, err := s.applyMasking(data, patterns)
if err != nil {
    slog.Error("Alert masking failed, continuing unmasked", "error", err)
    return data  // Return original
}
```

Alert data is user-provided — they already have access to it. Blocking alert submission due to a masking bug would be worse than processing unmasked data.

### Pattern Compilation Errors

Invalid regex patterns are logged and skipped at service creation:

```go
compiled, err := regexp.Compile(pattern.Pattern)
if err != nil {
    slog.Error("Failed to compile masking pattern, skipping",
        "pattern", name, "error", err)
    continue
}
```

### Code Masker Errors

Individual masker errors don't propagate — processing continues with remaining maskers:

```go
if masker.AppliesTo(masked) {
    result := masker.Mask(masked)
    // Mask() is defensive — returns original on internal error
    masked = result
}
```

---

## Implementation Plan

### Step 1: Core Masking Package

**Files**: `pkg/masking/masker.go`, `pkg/masking/pattern.go`, `pkg/masking/service.go`

1. Define `Masker` interface
2. Implement `CompiledPattern` and pattern compilation from `config.GetBuiltinConfig().MaskingPatterns`
3. Implement pattern group expansion from `config.GetBuiltinConfig().PatternGroups`
4. Implement `resolvePatterns()` for expanding `MaskingConfig` to concrete patterns
5. Implement `applyMasking()` — two-phase (code maskers → regex)
6. Implement `MaskToolResult()` with fail-closed behavior
7. Implement `MaskAlertData()` with fail-open behavior
8. Implement `NewMaskingService()` constructor

**Tests**: `pkg/masking/service_test.go`, `pkg/masking/pattern_test.go`
- Pattern compilation (valid, invalid, edge cases)
- Pattern group expansion (all groups, unknown groups)
- `MaskToolResult` with various server configs (enabled, disabled, nil)
- `MaskAlertData` with various pattern groups
- Fail-closed behavior (mock masking failure)
- Fail-open behavior for alerts
- Custom patterns from server config
- Empty content, no patterns configured

### Step 2: KubernetesSecretMasker

**Files**: `pkg/masking/kubernetes_secret.go`

1. Implement `AppliesTo()` — fast string check + regex
2. Implement `maskYAML()` — multi-document parsing, Secret detection, data field masking
3. Implement `maskJSON()` — tree walking, Secret detection, data/stringData masking
4. Implement `maskJSONInAnnotations()` — find embedded JSON Secrets in string values
5. Handle List/SecretList resources
6. Handle edge cases (malformed, mixed resources, empty data)

**Tests**: `pkg/masking/kubernetes_secret_test.go`
- YAML Secret with data fields → masked
- YAML ConfigMap with data fields → NOT masked
- Multi-document YAML with Secret + ConfigMap → only Secret masked
- JSON Secret → masked
- JSON List with mixed items → only Secrets masked
- JSON in YAML annotations → masked
- Malformed YAML/JSON → original returned
- Empty data → unchanged
- stringData field → masked

### Step 3: ToolExecutor Integration

**Files**: `pkg/mcp/executor.go`, `pkg/mcp/client_factory.go`

1. Add `maskingService` field to `ToolExecutor`
2. Update `NewToolExecutor` signature
3. Replace TODO stub in `Execute()` with `MaskToolResult()` call
4. Update `ClientFactory` to hold and pass masking service
5. Update all `NewToolExecutor` / `NewClientFactory` call sites

**Tests**: Update `pkg/mcp/executor_test.go`
- Tool execution with masking enabled → content masked
- Tool execution with masking disabled → content unchanged
- Tool execution with nil masking service → content unchanged
- Masking failure → `[REDACTED: data masking failure — tool result could not be safely processed]` returned

### Step 4: Alert Masking Integration

**Files**: `pkg/services/alert_service.go`, `pkg/config/defaults.go`

1. Add `AlertMaskingDefaults` type to `pkg/config/defaults.go`
2. Add `alert_masking` config to `Defaults`
3. Apply built-in defaults in `loader.go`
4. Add validation for `alert_masking.pattern_group`
5. Update `AlertService` constructor to accept `MaskingService`
6. Add masking call in `SubmitAlert()` before DB insert
7. Update YAML example config

**Tests**: Update `pkg/services/alert_service_test.go`
- Alert submission with masking enabled → stored data is masked
- Alert submission with masking disabled → stored data unchanged
- Alert submission with nil masking service → stored data unchanged

### Step 5: Wiring & Validation

**Files**: `cmd/tarsy/main.go` (or app initialization), `deploy/config/tarsy.yaml.example`

1. Create `MaskingService` at startup
2. Wire into `ClientFactory` and `AlertService`
3. Update YAML example with alert masking config
4. Update `initBuiltinCodeMaskers` comment (remove "TODO Phase 7" note — now implemented)

---

## Testing Strategy

### Unit Tests

| Area | Test File | Key Scenarios |
|------|-----------|---------------|
| Pattern compilation | `pattern_test.go` | All 15 built-in patterns compile; invalid regex skipped; group expansion |
| MaskingService | `service_test.go` | MaskToolResult, MaskAlertData, fail-closed, fail-open, config resolution |
| K8s Secret masker | `kubernetes_secret_test.go` | YAML/JSON Secrets masked; ConfigMaps untouched; mixed; malformed |
| Regex patterns | `pattern_test.go` | Each of 15 patterns with positive/negative test cases |

### Integration Tests

| Area | Test File | Key Scenarios |
|------|-----------|---------------|
| ToolExecutor + masking | `executor_test.go` | End-to-end: MCP result → masked ToolResult |
| AlertService + masking | `alert_service_test.go` | Submit alert → verify stored data is masked |

### Test Data Strategy

Use `testdata/` directory with realistic Kubernetes output:
- Single Secret (YAML and JSON)
- Single ConfigMap (should NOT be masked)
- Multi-document with mixed resources
- Secret with `last-applied-configuration` annotation containing JSON Secret
- Large tool output with embedded secrets in various formats

### Pattern Regression Tests

Table-driven tests for each of the 15 built-in patterns:

```go
func TestBuiltinPatterns(t *testing.T) {
    tests := []struct {
        pattern  string
        input    string
        expected string
    }{
        {"api_key", `api_key: "sk-1234567890abcdef1234"`, `"api_key": "__MASKED_API_KEY__"`},
        {"password", `password: "myS3cretP@ss"`, `"password": "__MASKED_PASSWORD__"`},
        // ... all 15 patterns
    }
}
```

---

## Comparison with Old TARSy

| Aspect | Old TARSy (Python) | New TARSy (Go) | Notes |
|--------|--------------------|--------------------|-------|
| Language | Python + Pydantic | Go + config structs | |
| Patterns | 9 regex + 1 code masker | 15 regex + 1 code masker | New: private_key, secret_key, aws_access_key, aws_secret_key, github_token, slack_token |
| Pattern groups | 5 groups | 6 groups (added "cloud") | |
| Service lifetime | Per-session (via MCPClient) | Singleton (compiled once) | Improvement: no redundant recompilation |
| MCP integration | `mask_response(dict, server)` | `MaskToolResult(string, serverID)` | Simpler: content is already a flat string in Go |
| Alert masking | env vars (ALERT_DATA_MASKING_*) | YAML config (defaults.alert_masking) | Improvement: consistent with rest of config |
| K8s masker loading | `importlib` dynamic import | Direct registration in constructor | Go doesn't need dynamic loading |
| Fail-safe (MCP) | `[REDACTED: masking failure]` | `[REDACTED: data masking failure — ...]` | Improved: explains why, no retry suggestion |
| Fail-safe (Alert) | Return original (fail-open) | Return original (fail-open) | Same behavior |
| Custom patterns | Compiled per-request | Compiled at startup | Improvement: no repeated compilation |
| Replacement format | `***MASKED_X***` | `__MASKED_X__` | Cosmetic difference (already in builtin.go) |
| Config structure | MaskingConfig on MCPServerConfig | Same structure | 1:1 mapping |

---

## Key Design Decisions

Resolved during design review (see `docs/phase4.2-data-masking-questions.md` for full context):

| Decision | Choice | Rationale |
|----------|--------|-----------|
| K8s Secret masker timing | Phase 4.2 | Project plan requires it; `kubernetes` pattern group is incomplete without it |
| MCP fail-safe behavior | Fail-closed with informative message (no retry suggestion) | Security over availability; no retry suggestion avoids loops |
| Service lifetime | Singleton (created once at startup) | Config is static; no reason to recompile patterns per session |
| Alert masking config | Under `defaults.alert_masking` in tarsy.yaml | Consistent with existing config hierarchy |
| Pattern compilation | Eager (all at startup) | Fail-fast; invalid patterns surface immediately |
| Pattern group namespace | Unified (regex + code maskers share namespace) | Already how `builtin.go` defines groups |
| Custom patterns scope | MCP server-level only (not chain-level) | Masking is about the data source, not the consumer |
| ReDoS protection | Rely on Go's RE2-safe `regexp` | Linear-time guarantee; no additional protection needed |

---

## Dependencies

### Go Standard Library Only

No new external dependencies required:
- `regexp` — pattern compilation and matching
- `encoding/json` — JSON parsing for K8s Secret masker
- `gopkg.in/yaml.v3` — YAML parsing for K8s Secret masker (already a dependency via config system)
- `strings` — fast string checks in `AppliesTo()`
- `log/slog` — structured logging
