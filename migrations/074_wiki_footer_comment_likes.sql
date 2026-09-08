CREATE TABLE wiki_footer_comment_likes (
 comment_id BIGINT NOT NULL REFERENCES wiki_footer_comments(id) ON DELETE CASCADE,
 user_id TEXT NOT NULL REFERENCES users(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(comment_id,user_id)
);

CREATE INDEX wiki_footer_comment_likes_user
 ON wiki_footer_comment_likes(user_id,comment_id);
