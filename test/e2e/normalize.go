package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Normalizer replaces dynamic values with stable placeholders for golden comparison.
// IDs that appear multiple times get the same placeholder (preserving referential integrity).
type Normalizer struct {
	sessionID string

	mu             sync.Mutex
	stageIDs       map[string]string // original → placeholder
	execIDs        map[string]string
	chatIDs        map[string]string
	interactionIDs map[string]string
	messageIDs     map[string]string

	stageCount       int
	execCount        int
	chatCount        int
	interactionCount int
	messageCount     int
}

// Regex patterns for dynamic values.
var (
	uuidRe       = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	timestampRe  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)
	unixTsRe     = regexp.MustCompile(`"(created_at|updated_at|started_at|completed_at|timestamp)":\s*\d{10,13}`)
	dbEventIDRe  = regexp.MustCompile(`"db_event_id":\s*\d+`)
	connIDRe     = regexp.MustCompile(`"connection_id":\s*"[^"]*"`)
	durationMsRe = regexp.MustCompile(`"duration_ms":\s*\d+`)
)

// NewNormalizer creates a normalizer that knows the session ID to replace.
func NewNormalizer(sessionID string) *Normalizer {
	return &Normalizer{
		sessionID:      sessionID,
		stageIDs:       make(map[string]string),
		execIDs:        make(map[string]string),
		chatIDs:        make(map[string]string),
		interactionIDs: make(map[string]string),
		messageIDs:     make(map[string]string),
	}
}

// RegisterStageID registers a stage UUID for stable replacement.
// Call this in order of first appearance.
func (n *Normalizer) RegisterStageID(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.stageIDs[id]; !ok {
		n.stageCount++
		n.stageIDs[id] = fmt.Sprintf("{STAGE_ID_%d}", n.stageCount)
	}
}

// RegisterExecutionID registers an execution UUID for stable replacement.
func (n *Normalizer) RegisterExecutionID(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.execIDs[id]; !ok {
		n.execCount++
		n.execIDs[id] = fmt.Sprintf("{EXEC_ID_%d}", n.execCount)
	}
}

// RegisterChatID registers a chat UUID for stable replacement.
func (n *Normalizer) RegisterChatID(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.chatIDs[id]; !ok {
		n.chatCount++
		n.chatIDs[id] = fmt.Sprintf("{CHAT_ID_%d}", n.chatCount)
	}
}

// RegisterInteractionID registers an interaction UUID for stable replacement.
func (n *Normalizer) RegisterInteractionID(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.interactionIDs[id]; !ok {
		n.interactionCount++
		n.interactionIDs[id] = fmt.Sprintf("{INTERACTION_ID_%d}", n.interactionCount)
	}
}

// RegisterMessageID registers a message UUID for stable replacement.
func (n *Normalizer) RegisterMessageID(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.messageIDs[id]; !ok {
		n.messageCount++
		n.messageIDs[id] = fmt.Sprintf("{MSG_ID_%d}", n.messageCount)
	}
}

// Normalize replaces dynamic values in data with stable placeholders.
func (n *Normalizer) Normalize(data string) string {
	n.mu.Lock()
	defer n.mu.Unlock()

	// 1. Replace known session ID first.
	if n.sessionID != "" {
		data = strings.ReplaceAll(data, n.sessionID, "{SESSION_ID}")
	}

	// 2. Replace registered stage IDs.
	for id, placeholder := range n.stageIDs {
		data = strings.ReplaceAll(data, id, placeholder)
	}

	// 3. Replace registered execution IDs.
	for id, placeholder := range n.execIDs {
		data = strings.ReplaceAll(data, id, placeholder)
	}

	// 4. Replace registered chat IDs.
	for id, placeholder := range n.chatIDs {
		data = strings.ReplaceAll(data, id, placeholder)
	}

	// 5. Replace registered interaction IDs.
	for id, placeholder := range n.interactionIDs {
		data = strings.ReplaceAll(data, id, placeholder)
	}

	// 6. Replace registered message IDs.
	for id, placeholder := range n.messageIDs {
		data = strings.ReplaceAll(data, id, placeholder)
	}

	// 7. Replace any remaining UUIDs.
	data = uuidRe.ReplaceAllString(data, "{UUID}")

	// 8. Replace RFC3339 timestamps.
	data = timestampRe.ReplaceAllString(data, "{TIMESTAMP}")

	// 9. Replace Unix timestamps in known fields.
	data = unixTsRe.ReplaceAllStringFunc(data, func(match string) string {
		// Keep the field name, replace the value.
		idx := strings.Index(match, ":")
		return match[:idx+1] + " {UNIX_TS}"
	})

	// 10. Replace db_event_id.
	data = dbEventIDRe.ReplaceAllString(data, `"db_event_id": {DB_EVENT_ID}`)

	// 11. Replace connection_id.
	data = connIDRe.ReplaceAllString(data, `"connection_id": "{CONN_ID}"`)

	// 12. Replace duration_ms (non-deterministic timing).
	data = durationMsRe.ReplaceAllString(data, `"duration_ms": {DURATION_MS}`)

	return data
}

// NormalizeBytes is a convenience wrapper for Normalize on byte slices.
func (n *Normalizer) NormalizeBytes(data []byte) []byte {
	return []byte(n.Normalize(string(data)))
}
