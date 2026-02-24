-- rename "iteration_strategy" column to "llm_backend" on "agent_executions" table
ALTER TABLE "public"."agent_executions" RENAME COLUMN "iteration_strategy" TO "llm_backend";
