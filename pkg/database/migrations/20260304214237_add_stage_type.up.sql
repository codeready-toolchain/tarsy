-- drop index "agentexecution_parent_execution_id_agent_index_sub_agent" from table: "agent_executions"
DROP INDEX "public"."agentexecution_parent_execution_id_agent_index_sub_agent";
-- drop index "agentexecution_stage_id_agent_index_top_level" from table: "agent_executions"
DROP INDEX "public"."agentexecution_stage_id_agent_index_top_level";
-- drop index "sessionscore_session_id" from table: "session_scores"
DROP INDEX "public"."sessionscore_session_id";
-- create index "sessionscore_session_id" to table: "session_scores"
CREATE UNIQUE INDEX "sessionscore_session_id" ON "public"."session_scores" ("session_id") WHERE ((status)::text = ANY ((ARRAY['pending'::character varying, 'in_progress'::character varying])::text[]));
-- modify "stages" table
ALTER TABLE "public"."stages" ADD COLUMN "stage_type" character varying NOT NULL DEFAULT 'investigation';
-- Backfill synthesis stages (identified by name suffix)
UPDATE stages SET stage_type = 'synthesis' WHERE stage_name LIKE '% - Synthesis';
-- Backfill chat stages (identified by non-null chat_id)
UPDATE stages SET stage_type = 'chat' WHERE chat_id IS NOT NULL;
