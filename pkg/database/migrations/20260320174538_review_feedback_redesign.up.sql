-- ============================================================
-- Review Feedback Redesign Migration
--
-- Replaces resolution_reason/resolution_note with
-- quality_rating/action_taken/investigation_feedback.
-- Renames resolved->reviewed, resolve->complete, update_note->update_feedback.
--
-- All enum columns are character varying (no PG enum types).
-- ============================================================

-- ── alert_sessions ──

-- 1. Rename review_status value: resolved -> reviewed.
UPDATE "public"."alert_sessions" SET review_status = 'reviewed' WHERE review_status = 'resolved';

-- 2. Add new columns.
ALTER TABLE "public"."alert_sessions"
    ADD COLUMN "quality_rating" character varying NULL,
    ADD COLUMN "action_taken" text NULL,
    ADD COLUMN "investigation_feedback" text NULL;

-- 3. Copy resolution_note -> action_taken before dropping.
UPDATE "public"."alert_sessions" SET action_taken = resolution_note WHERE resolution_note IS NOT NULL;

-- 4. Backfill quality_rating=accurate for human-reviewed sessions.
UPDATE "public"."alert_sessions"
    SET quality_rating = 'accurate'
    WHERE review_status = 'reviewed' AND assignee IS NOT NULL;

-- 5. Rename resolved_at -> reviewed_at.
ALTER TABLE "public"."alert_sessions" RENAME COLUMN resolved_at TO reviewed_at;

-- 6. Drop old columns.
ALTER TABLE "public"."alert_sessions"
    DROP COLUMN "resolution_reason",
    DROP COLUMN "resolution_note";

-- ── session_review_activities ──

-- 7. Rename action values: resolve -> complete, update_note -> update_feedback.
UPDATE "public"."session_review_activities" SET action = 'complete' WHERE action = 'resolve';
UPDATE "public"."session_review_activities" SET action = 'update_feedback' WHERE action = 'update_note';

-- 8. Rename from_status/to_status values: resolved -> reviewed.
UPDATE "public"."session_review_activities" SET from_status = 'reviewed' WHERE from_status = 'resolved';
UPDATE "public"."session_review_activities" SET to_status = 'reviewed' WHERE to_status = 'resolved';

-- 9. Add new columns.
ALTER TABLE "public"."session_review_activities"
    ADD COLUMN "quality_rating" character varying NULL,
    ADD COLUMN "investigation_feedback" text NULL;

-- 10. Drop old column.
ALTER TABLE "public"."session_review_activities"
    DROP COLUMN "resolution_reason";
