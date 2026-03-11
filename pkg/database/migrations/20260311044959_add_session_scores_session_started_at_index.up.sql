-- create index "sessionscore_session_id_started_at" to table: "session_scores"
CREATE INDEX "sessionscore_session_id_started_at" ON "public"."session_scores" ("session_id", "started_at");
