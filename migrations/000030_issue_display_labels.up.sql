ALTER TABLE issues
    ADD COLUMN display_title   text NOT NULL DEFAULT '',
    ADD COLUMN display_culprit text NOT NULL DEFAULT '';
