-- Add parent_execution_id to timeline_events for orchestrator sub-agent event tracking.
-- Nullable: NULL for regular agents and orchestrators, set for sub-agent events.
ALTER TABLE "public"."timeline_events"
    ADD COLUMN "parent_execution_id" character varying NULL,
    ADD CONSTRAINT "timeline_events_agent_executions_sub_agent_timeline_events"
        FOREIGN KEY ("parent_execution_id")
        REFERENCES "public"."agent_executions" ("execution_id")
        ON UPDATE NO ACTION ON DELETE SET NULL;

-- Index for efficient sub-agent event lookups by parent orchestrator
CREATE INDEX "timelineevent_parent_execution_id" ON "public"."timeline_events" ("parent_execution_id");
