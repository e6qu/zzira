CREATE TABLE wiki_footer_comments (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 page_id BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
 parent_id BIGINT REFERENCES wiki_footer_comments(id) ON DELETE CASCADE,
 body TEXT NOT NULL,
 author_id TEXT NOT NULL REFERENCES users(id),
 version INT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX wiki_footer_comments_page_parent
 ON wiki_footer_comments(page_id,parent_id,created_at,id);

CREATE TABLE wiki_footer_comment_versions (
 comment_id BIGINT NOT NULL REFERENCES wiki_footer_comments(id) ON DELETE CASCADE,
 version INT NOT NULL,
 body TEXT NOT NULL,
 author_id TEXT NOT NULL REFERENCES users(id),
 message TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(comment_id,version)
);
