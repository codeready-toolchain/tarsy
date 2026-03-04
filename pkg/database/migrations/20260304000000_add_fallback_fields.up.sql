-- Add fallback tracking fields to agent_executions.
-- Both are nullable: NULL means no fallback occurred during this execution.
ALTER TABLE "public"."agent_executions"
    ADD COLUMN "original_llm_provider" character varying NULL,
    ADD COLUMN "original_llm_backend" character varying NULL;

-- Add provider_fallback to timeline event_type enum.
ALTER TYPE "public"."timelineevent_event_type"
    ADD VALUE IF NOT EXISTS 'provider_fallback';
