-- Add session_id index to mcp_interactions for dashboard aggregation queries.
-- Matches the existing llm_interactions pattern (session_id, created_at).
CREATE INDEX IF NOT EXISTS "mcpinteraction_session_id_created_at"
    ON "public"."mcp_interactions" ("session_id", "created_at");
