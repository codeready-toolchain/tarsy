-- ============================================================
-- Investigation Memory
--
-- Enables pgvector, creates the investigation_memories table
-- (with embedding column + HNSW index), the M2M join table
-- for injected memories, and adds memory_extraction to the
-- llm_interactions interaction_type enum.
-- ============================================================

BEGIN;

-- 1. Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- 2. Create investigation_memories table (Ent-managed columns)
CREATE TABLE "public"."investigation_memories" (
  "memory_id" character varying NOT NULL,
  "project" character varying NOT NULL DEFAULT 'default',
  "content" text NOT NULL,
  "category" character varying NOT NULL,
  "valence" character varying NOT NULL,
  "confidence" double precision NOT NULL DEFAULT 0.5,
  "seen_count" bigint NOT NULL DEFAULT 1,
  "alert_type" character varying NULL,
  "chain_id" character varying NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "last_seen_at" timestamptz NOT NULL,
  "deprecated" boolean NOT NULL DEFAULT false,
  "source_session_id" character varying NOT NULL,
  PRIMARY KEY ("memory_id"),
  CONSTRAINT "investigation_memories_alert_sessions_memories"
    FOREIGN KEY ("source_session_id")
    REFERENCES "public"."alert_sessions" ("session_id")
    ON UPDATE NO ACTION ON DELETE CASCADE
);

-- 3. Ent-defined indexes
CREATE INDEX "investigationmemory_project"
  ON "public"."investigation_memories" ("project");

CREATE INDEX "investigationmemory_project_deprecated"
  ON "public"."investigation_memories" ("project", "deprecated");

CREATE INDEX "investigationmemory_source_session_id"
  ON "public"."investigation_memories" ("source_session_id");

CREATE INDEX "investigationmemory_category"
  ON "public"."investigation_memories" ("category");

-- 4. Embedding column (pgvector type — not managed by Ent).
-- Intentionally nullable: Ent cannot manage pgvector types, so the embedding
-- is set via a raw SQL UPDATE immediately after the Ent record is created.
-- If the embedding API fails, the record exists without an embedding.
-- Similarity queries filter with "embedding IS NOT NULL" to handle this.
ALTER TABLE "public"."investigation_memories"
  ADD COLUMN "embedding" vector(768);

-- 5. HNSW index for approximate nearest-neighbor cosine search
CREATE INDEX "idx_investigation_memories_embedding"
  ON "public"."investigation_memories"
  USING hnsw ("embedding" vector_cosine_ops)
  WITH (m = 16, ef_construction = 64);

-- 6. M2M join table for injected memories
CREATE TABLE "public"."alert_session_injected_memories" (
  "alert_session_id" character varying NOT NULL,
  "investigation_memory_id" character varying NOT NULL,
  PRIMARY KEY ("alert_session_id", "investigation_memory_id"),
  CONSTRAINT "alert_session_injected_memories_alert_session_id"
    FOREIGN KEY ("alert_session_id")
    REFERENCES "public"."alert_sessions" ("session_id")
    ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "alert_session_injected_memories_investigation_memory_id"
    FOREIGN KEY ("investigation_memory_id")
    REFERENCES "public"."investigation_memories" ("memory_id")
    ON UPDATE NO ACTION ON DELETE CASCADE
);

COMMIT;
