-- 平台设置：会话签名密钥等。时间统一存 Unix 秒，便于以后换 PostgreSQL。
CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);

-- 一次性登录链接：库里只存令牌的 SHA-256，用过或过期即失效。
CREATE TABLE login_tokens (
    token_hash TEXT    PRIMARY KEY,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);
