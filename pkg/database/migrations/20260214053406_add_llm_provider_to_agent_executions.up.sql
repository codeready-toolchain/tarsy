-- modify "agent_executions" table
ALTER TABLE "public"."agent_executions" ADD COLUMN "llm_provider" character varying NULL;
-- modify "llm_interactions" table
ALTER TABLE "public"."llm_interactions" ALTER COLUMN "execution_id" DROP NOT NULL, ALTER COLUMN "stage_id" DROP NOT NULL;
-- create index "llminteraction_session_id_created_at" to table: "llm_interactions"
CREATE INDEX "llminteraction_session_id_created_at" ON "public"."llm_interactions" ("session_id", "created_at");
