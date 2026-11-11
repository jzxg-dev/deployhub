-- 凭据：成组的登录信息（如 Cloudflare 账号）。不敏感的字段明文保存，敏感字段合成 JSON 后加密保存。
CREATE TABLE credentials (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    kind        TEXT    NOT NULL,
    title       TEXT    NOT NULL DEFAULT '',
    fields      TEXT    NOT NULL DEFAULT '{}', -- 不敏感的字段（JSON 对象）
    secrets_enc TEXT    NOT NULL DEFAULT '',   -- 敏感字段（JSON 对象，加密后）
    info        TEXT,                          -- 最近一次验证结果（JSON），没验证过为 NULL
    checked_at  INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
