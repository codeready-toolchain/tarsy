BEGIN;

-- Add nullable cache token counts reported by LLM providers.
ALTER TABLE "public"."llm_interactions"
    ADD COLUMN "cache_read_tokens" bigint NULL,
    ADD COLUMN "cache_creation_tokens" bigint NULL;

COMMIT;
