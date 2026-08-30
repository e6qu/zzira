INSERT INTO statuses (id, name, category) VALUES
  ('st_todo',       'To Do',       'new'),
  ('st_inprogress', 'In Progress', 'indeterminate'),
  ('st_done',       'Done',        'done');

INSERT INTO issue_types (id, name, icon) VALUES
  ('it_task', 'Task', 'task');

INSERT INTO priorities (id, name) VALUES
  ('pr_medium', 'Medium');

INSERT INTO workspaces (id, slug, name) VALUES
  ('ws_default', 'zzira', 'ZZIRA');

INSERT INTO projects (id, workspace_id, key, name) VALUES
  ('prj_default', 'ws_default', 'ZZ', 'ZZIRA Demo');
