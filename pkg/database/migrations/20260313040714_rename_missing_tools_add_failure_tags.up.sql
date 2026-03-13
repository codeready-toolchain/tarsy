-- rename "missing_tools_analysis" to "tool_improvement_report" in "session_scores" table
ALTER TABLE "public"."session_scores" RENAME COLUMN "missing_tools_analysis" TO "tool_improvement_report";
-- add "failure_tags" column to "session_scores" table
ALTER TABLE "public"."session_scores" ADD COLUMN "failure_tags" jsonb NULL;
