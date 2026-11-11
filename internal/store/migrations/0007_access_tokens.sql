-- 访问令牌：给 AI 和脚本用（HTTP API 的 Authorization: Bearer、MCP）。库里只存令牌的 SHA-256，明文只在创建时显示一次。
-- level：read 只读 / deploy 可部署 / admin 管理。
CREATE TABLE access_tokens (
    id           INTEGER PRIMARY KEY,
    owner_id     INTEGER NOT NULL,
    name         TEXT    NOT NULL,
    level        TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL UNIQUE,
    hint         TEXT    NOT NULL DEFAULT '',  -- 令牌开头的几个字符，页面上用来辨认
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    UNIQUE (owner_id, name)
);

-- 任务：幂等键（同一个键重复提交时返回原来的任务）、批量部署时某个应用失败后是否继续（待审批的任务批准后要用）。
-- 任务状态新增 pending_approval（待审批）和 rejected（已拒绝）。
ALTER TABLE jobs ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN continue_on_error INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX jobs_idempotency ON jobs (owner_id, idempotency_key) WHERE idempotency_key <> '';
