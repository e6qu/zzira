CREATE TABLE IF NOT EXISTS workflows (
  id   TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  def  JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE projects ADD COLUMN IF NOT EXISTS workflow_id TEXT REFERENCES workflows(id);

INSERT INTO workflows (id, name, def) VALUES
  ('wf_default', 'Default',
   jsonb_build_object(
     'id','wf_default', 'name','Default',
     'transitions', jsonb_build_array(
       jsonb_build_object('id','11','name','To Do','from',jsonb_build_array('st_inprogress','st_done'),'to','st_todo'),
       jsonb_build_object('id','21','name','In Progress','from',jsonb_build_array('st_todo','st_done'),'to','st_inprogress'),
       jsonb_build_object('id','31','name','Done','from',jsonb_build_array('st_todo','st_inprogress'),'to','st_done')
     )
   ))
ON CONFLICT (id) DO NOTHING;
