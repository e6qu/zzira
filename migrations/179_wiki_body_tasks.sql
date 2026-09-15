-- Tasks live in the bodies of pages and blog posts, as Confluence keeps them,
-- and blog posts can hold tasks too.
ALTER TABLE wiki_tasks ADD COLUMN blog_post_id bigint REFERENCES wiki_blog_posts(id) ON DELETE CASCADE;
ALTER TABLE wiki_tasks ALTER COLUMN page_id DROP NOT NULL;
ALTER TABLE wiki_tasks ADD CONSTRAINT wiki_tasks_container CHECK (num_nonnulls(page_id, blog_post_id) = 1);
CREATE UNIQUE INDEX wiki_tasks_blog_post_local ON wiki_tasks (blog_post_id, local_id) WHERE blog_post_id IS NOT NULL;

-- Tasks added before now were kept beside their pages. Write each one into
-- its page's body as a task list, with its assignee as a mention and its due
-- date as a date, so the body is where every task is.
WITH lists AS (
  SELECT t.page_id,
    '<ac:task-list>' || string_agg(
      '<ac:task><ac:task-id>' || replace(replace(replace(t.local_id, '&', '&amp;'), '<', '&lt;'), '>', '&gt;') || '</ac:task-id>'
      || '<ac:task-status>' || t.status || '</ac:task-status><ac:task-body>' || t.body
      || CASE WHEN t.assigned_to IS NULL THEN '' ELSE ' <ac:link><ri:user ri:account-id="' || replace(replace(t.assigned_to, '&', '&amp;'), '"', '&quot;') || '" /></ac:link>' END
      || CASE WHEN t.due_at IS NULL THEN '' ELSE ' <time datetime="' || to_char(t.due_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') || '" />' END
      || '</ac:task-body></ac:task>', '' ORDER BY t.created_at, t.id) || '</ac:task-list>' AS markup
  FROM wiki_tasks t
  JOIN wiki_pages p ON p.id = t.page_id
  WHERE position('<ac:task-id>' || t.local_id || '</ac:task-id>' IN p.body) = 0
  GROUP BY t.page_id
), written AS (
  UPDATE wiki_pages p SET body = p.body || lists.markup
  FROM lists WHERE p.id = lists.page_id
  RETURNING p.id, p.version, p.body
)
UPDATE wiki_page_versions v SET body = written.body
FROM written WHERE v.page_id = written.id AND v.version = written.version;

-- A task due on a day is due at the start of that day.
UPDATE wiki_tasks SET due_at = date_trunc('day', due_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' WHERE due_at IS NOT NULL;
