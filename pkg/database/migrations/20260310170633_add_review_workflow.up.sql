-- modify "alert_sessions" table
ALTER TABLE "public"."alert_sessions" ADD COLUMN "review_status" character varying NULL, ADD COLUMN "assignee" character varying NULL, ADD COLUMN "assigned_at" timestamptz NULL, ADD COLUMN "resolved_at" timestamptz NULL, ADD COLUMN "resolution_reason" character varying NULL, ADD COLUMN "resolution_note" text NULL;
-- create index "alertsession_assignee" to table: "alert_sessions"
CREATE INDEX "alertsession_assignee" ON "public"."alert_sessions" ("assignee");
-- create index "alertsession_review_status" to table: "alert_sessions"
CREATE INDEX "alertsession_review_status" ON "public"."alert_sessions" ("review_status");
-- create index "alertsession_review_status_assignee" to table: "alert_sessions"
CREATE INDEX "alertsession_review_status_assignee" ON "public"."alert_sessions" ("review_status", "assignee");
-- create "session_review_activities" table
CREATE TABLE "public"."session_review_activities" (
  "activity_id" character varying NOT NULL,
  "actor" character varying NOT NULL,
  "action" character varying NOT NULL,
  "from_status" character varying NULL,
  "to_status" character varying NOT NULL,
  "resolution_reason" character varying NULL,
  "note" text NULL,
  "created_at" timestamptz NOT NULL,
  "session_id" character varying NOT NULL,
  PRIMARY KEY ("activity_id"),
  CONSTRAINT "session_review_activities_alert_sessions_review_activities" FOREIGN KEY ("session_id") REFERENCES "public"."alert_sessions" ("session_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "sessionreviewactivity_session_id_created_at" to table: "session_review_activities"
CREATE INDEX "sessionreviewactivity_session_id_created_at" ON "public"."session_review_activities" ("session_id", "created_at");
-- backfill: mark all existing terminal sessions as resolved/dismissed
UPDATE "public"."alert_sessions"
SET review_status = 'resolved',
    resolution_reason = 'dismissed',
    resolved_at = completed_at
WHERE status IN ('completed', 'failed', 'timed_out', 'cancelled')
  AND review_status IS NULL;
