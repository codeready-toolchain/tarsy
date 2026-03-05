-- modify "stages" table
ALTER TABLE "public"."stages" ADD COLUMN "referenced_stage_id" character varying NULL, ADD CONSTRAINT "stages_stages_referencing_stages" FOREIGN KEY ("referenced_stage_id") REFERENCES "public"."stages" ("stage_id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Backfill: pair existing synthesis stages to their parent investigation stage
UPDATE stages s_synth
SET referenced_stage_id = (
    SELECT s_inv.stage_id
    FROM stages s_inv
    WHERE s_inv.session_id = s_synth.session_id
      AND s_inv.stage_type = 'investigation'
      AND s_inv.stage_name = REPLACE(s_synth.stage_name, ' - Synthesis', '')
      AND s_inv.stage_index < s_synth.stage_index
    ORDER BY s_inv.stage_index DESC
    LIMIT 1
)
WHERE s_synth.stage_type = 'synthesis';
