ALTER TABLE projects
    ADD COLUMN source_link_template text NOT NULL DEFAULT '',
    ADD COLUMN source_stack_root text NOT NULL DEFAULT '',
    ADD COLUMN source_root text NOT NULL DEFAULT '';
