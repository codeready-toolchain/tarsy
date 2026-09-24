BEGIN;
ALTER TABLE "public"."alert_sessions"
  ADD COLUMN "cancelled_by" character varying NULL,
  ADD COLUMN "cancel_reason" text NULL;
COMMIT;
