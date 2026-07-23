BEGIN;

-- Add nullable thinking token count and point-in-time estimated USD cost.
ALTER TABLE "public"."llm_interactions"
    ADD COLUMN "thinking_tokens" bigint NULL,
    ADD COLUMN "estimated_cost_usd" double precision NULL;

COMMIT;
