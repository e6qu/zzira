CREATE TABLE wiki_labels (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id),
 prefix TEXT NOT NULL CHECK(prefix IN ('global','my','team','system')),
 name TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(workspace_id,prefix,name)
);

CREATE TABLE wiki_page_labels (
 page_id BIGINT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
 label_id BIGINT NOT NULL REFERENCES wiki_labels(id) ON DELETE CASCADE,
 author_id TEXT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(page_id,label_id)
);

CREATE TABLE wiki_space_labels (
 space_id BIGINT NOT NULL REFERENCES wiki_spaces(id) ON DELETE CASCADE,
 label_id BIGINT NOT NULL REFERENCES wiki_labels(id) ON DELETE CASCADE,
 author_id TEXT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(space_id,label_id)
);

CREATE INDEX wiki_labels_workspace_name ON wiki_labels(workspace_id,name,id);
CREATE INDEX wiki_page_labels_label ON wiki_page_labels(label_id,page_id);
CREATE INDEX wiki_space_labels_label ON wiki_space_labels(label_id,space_id);
