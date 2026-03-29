-- ============================================================
-- Entity-Level Recall
--
-- Adds a stored tsvector column to alert_sessions for
-- keyword-based full-text search on alert_data. Used by the
-- search_past_sessions tool to find sessions matching specific
-- entity identifiers (usernames, namespaces, workloads).
--
-- 'simple' config preserves identifiers without stemming so
-- usernames, namespace names, and workload names stay exact.
-- GENERATED ALWAYS keeps the column in sync with alert_data
-- automatically — no application-level maintenance.
-- ============================================================

BEGIN;

ALTER TABLE alert_sessions
  ADD COLUMN search_vector tsvector
  GENERATED ALWAYS AS (to_tsvector('simple', alert_data)) STORED;

CREATE INDEX idx_sessions_search
  ON alert_sessions USING gin(search_vector);

COMMIT;
