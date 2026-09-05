CREATE TABLE wiki_spaces (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id),
 key TEXT NOT NULL,
 name TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 author_id TEXT NOT NULL REFERENCES users(id),
 private BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,key)
);
CREATE TABLE wiki_pages (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 space_id BIGINT NOT NULL REFERENCES wiki_spaces(id),
 parent_id BIGINT REFERENCES wiki_pages(id),
 title TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('current','draft','trashed')),
 body TEXT NOT NULL DEFAULT '',
 author_id TEXT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 version INT NOT NULL DEFAULT 1
);
CREATE INDEX wiki_pages_space ON wiki_pages(space_id,status,id);
CREATE TABLE wiki_page_versions (
 page_id BIGINT NOT NULL REFERENCES wiki_pages(id),
 version INT NOT NULL,
 title TEXT NOT NULL,
 body TEXT NOT NULL,
 status TEXT NOT NULL,
 author_id TEXT NOT NULL REFERENCES users(id),
 message TEXT NOT NULL DEFAULT '',
 minor_edit BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(page_id,version)
);

CREATE UNIQUE INDEX wiki_page_current_title ON wiki_pages(space_id,title) WHERE status='current';
