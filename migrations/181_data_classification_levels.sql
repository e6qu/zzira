-- Data classification levels belong to the organization: its administrators
-- define, publish, archive and order them, and Jira projects and Confluence
-- content across its sites use them. Every organization starts with the four
-- levels this product shipped, under the ids its content already carries.
CREATE TABLE data_classification_levels (
  organization_id text NOT NULL,
  id text NOT NULL,
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 255),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
  guideline text NOT NULL DEFAULT '' CHECK (length(guideline) <= 4000),
  color text NOT NULL CHECK (color IN ('RED', 'RED_BOLD', 'ORANGE', 'YELLOW', 'GREEN', 'BLUE', 'NAVY', 'TEAL', 'PURPLE', 'GREY', 'LIME')),
  status text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'PUBLISHED', 'ARCHIVED')),
  rank integer NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (organization_id, id)
);
CREATE UNIQUE INDEX data_classification_levels_name ON data_classification_levels (organization_id, lower(name));

INSERT INTO data_classification_levels (organization_id, id, name, description, guideline, color, status, rank)
SELECT o.id::text, level.id, level.name, level.description, level.guideline, level.color, 'PUBLISHED', level.rank
FROM organizations o
CROSS JOIN (VALUES
  ('public', 'Public', 'Approved for public sharing', 'May be shared outside the organization.', 'GREEN', 0),
  ('internal', 'Internal', 'For organization members', 'Share only with authenticated organization members.', 'BLUE', 1),
  ('confidential', 'Confidential', 'Limited business information', 'Share only with people who need this information.', 'ORANGE', 2),
  ('restricted', 'Restricted', 'Highly sensitive information', 'Use explicit access controls and approved handling.', 'RED_BOLD', 3)
) AS level(id, name, description, guideline, color, rank)
ON CONFLICT DO NOTHING;

-- Which levels exist is the organization's to decide, so the column no longer
-- names them.
ALTER TABLE wiki_spaces DROP CONSTRAINT IF EXISTS wiki_spaces_default_classification_level_check;
