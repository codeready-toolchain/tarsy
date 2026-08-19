BEGIN;

-- Persist configured model on agent executions (and original model on fallback).
ALTER TABLE "public"."agent_executions"
    ADD COLUMN "model_name" character varying NULL,
    ADD COLUMN "original_model_name" character varying NULL;

COMMIT;
