-- drop index "agentexecution_stage_id_agent_index" from table: "agent_executions"
DROP INDEX "public"."agentexecution_stage_id_agent_index";
-- modify "agent_executions" table
ALTER TABLE "public"."agent_executions" ADD COLUMN "task" text NULL, ADD COLUMN "parent_execution_id" character varying NULL, ADD CONSTRAINT "agent_executions_agent_executions_sub_agents" FOREIGN KEY ("parent_execution_id") REFERENCES "public"."agent_executions" ("execution_id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- create index "agentexecution_parent_execution_id" to table: "agent_executions"
CREATE INDEX "agentexecution_parent_execution_id" ON "public"."agent_executions" ("parent_execution_id");
-- Top-level agents: unique within stage (preserves original semantics)
CREATE UNIQUE INDEX "agentexecution_stage_id_agent_index_top_level"
    ON "public"."agent_executions" ("stage_id", "agent_index")
    WHERE parent_execution_id IS NULL;
-- Sub-agents: unique within parent orchestrator
CREATE UNIQUE INDEX "agentexecution_parent_execution_id_agent_index_sub_agent"
    ON "public"."agent_executions" ("parent_execution_id", "agent_index")
    WHERE parent_execution_id IS NOT NULL;
