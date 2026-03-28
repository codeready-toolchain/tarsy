-- ============================================================
-- Hybrid Search — Phase 3
--
-- Adds a stored tsvector column to investigation_memories for
-- keyword-based full-text search. Used alongside the existing
-- pgvector embedding for hybrid (vector + keyword) retrieval
-- via Reciprocal Rank Fusion (RRF).
--
-- 'simple' config preserves identifiers without stemming so
-- tool names, namespaces, and workload names stay exact.
-- GENERATED ALWAYS keeps the column in sync with content
-- automatically — no application-level maintenance.
-- ============================================================

BEGIN;

ALTER TABLE investigation_memories
  ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED;

CREATE INDEX idx_memories_search
  ON investigation_memories USING gin(search_vector);

COMMIT;
