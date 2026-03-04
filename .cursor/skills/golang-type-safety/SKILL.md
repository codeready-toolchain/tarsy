---
name: golang-type-safety
description: Prefer typed constants and named types over magic strings/numbers in Go. Use when writing function signatures, struct fields, switch statements, or passing enum-like values.
---

# Go Type Safety

Use typed constants and named types instead of magic strings and numbers. This catches misuse at compile time and makes intent explicit.

## Named String Types for Enums

When a value comes from a defined set (e.g. ent-generated enums, config enums), use the named type:

```go
// Bad — magic string, no compile-time checking
publishStageStatus(ctx, pub, sid, stgID, name, idx, "investigation", status)

// Good — typed constant, typos caught at compile time
publishStageStatus(ctx, pub, sid, stgID, name, idx, stage.StageTypeInvestigation, status)
```

## Function Signatures

Accept the named type, not `string`, when the parameter is always one of a known set:

```go
// Bad — caller can pass any string
func publishStageStatus(ctx context.Context, ..., stageType string, status string)

// Good — compiler enforces valid stage types
func publishStageStatus(ctx context.Context, ..., stageType stage.StageType, status string)
```

Convert to `string` at serialization boundaries (JSON payloads, DB queries, log messages):

```go
payload.StageType = string(stageType)
```

## Struct Fields

Use `string` for DTOs/payloads that cross API boundaries (JSON serialization).
Use the named type for internal structs where type safety helps:

```go
// API payload — string is fine (wire format)
type StageStatusPayload struct {
    StageType string `json:"stage_type"`
}

// Internal struct — use the named type
type stageInput struct {
    stageType stage.StageType
}
```

## Struct Literals

When constructing structs with string fields that accept enum values, use `string(constant)`:

```go
// Bad
req := CreateStageRequest{StageType: "synthesis"}

// Good
req := CreateStageRequest{StageType: string(stage.StageTypeSynthesis)}
```

## When NOT to Use Named Types

- One-off values that aren't from a defined set (free-form names, user input)
- Test data where the literal is the point (e.g. `"bogus"` for invalid input tests)
- String comparisons in tests asserting wire-format output
