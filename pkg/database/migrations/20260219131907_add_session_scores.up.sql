-- create "session_scores" table
CREATE TABLE "public"."session_scores" (
  "score_id" character varying NOT NULL,
  "prompt_hash" character varying NULL,
  "total_score" bigint NULL,
  "score_analysis" text NULL,
  "missing_tools_analysis" text NULL,
  "score_triggered_by" character varying NOT NULL,
  "status" character varying NOT NULL DEFAULT 'pending',
  "started_at" timestamptz NOT NULL,
  "completed_at" timestamptz NULL,
  "error_message" text NULL,
  "session_id" character varying NOT NULL,
  PRIMARY KEY ("score_id"),
  CONSTRAINT "session_scores_alert_sessions_session_scores" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "sessionscore_prompt_hash" to table: "session_scores"
CREATE INDEX "sessionscore_prompt_hash" ON "public"."session_scores" ("prompt_hash");
-- create index "sessionscore_session_id" to table: "session_scores"
CREATE UNIQUE INDEX "sessionscore_session_id" ON "public"."session_scores" ("session_id") WHERE ((status)::text = ANY ((ARRAY['pending'::character varying, 'in_progress'::character varying])::text[]));
-- create index "sessionscore_session_id_status" to table: "session_scores"
CREATE INDEX "sessionscore_session_id_status" ON "public"."session_scores" ("session_id", "status");
-- create index "sessionscore_status" to table: "session_scores"
CREATE INDEX "sessionscore_status" ON "public"."session_scores" ("status");
-- create index "sessionscore_status_started_at" to table: "session_scores"
CREATE INDEX "sessionscore_status_started_at" ON "public"."session_scores" ("status", "started_at");
-- create index "sessionscore_total_score" to table: "session_scores"
CREATE INDEX "sessionscore_total_score" ON "public"."session_scores" ("total_score");
