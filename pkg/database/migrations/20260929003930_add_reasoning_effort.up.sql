BEGIN;

-- Persist the effective reasoning effort next to the model at call time.
-- NULL matches historical rows and calls that sent no effort.
ALTER TABLE "public"."agent_executions"
    ADD COLUMN "reasoning_effort" character varying NULL,
    ADD COLUMN "original_reasoning_effort" character varying NULL;

ALTER TABLE "public"."llm_interactions"
    ADD COLUMN "reasoning_effort" character varying NULL;

COMMIT;
