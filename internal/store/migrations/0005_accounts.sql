-- migrate:no-foreign-keys
-- 账号和按账号隔离：服务器、密钥、项目、凭据、任务都属于某个账号（owner_id），名称只在账号内唯一。
-- 原来的名称是全局唯一（列上的 UNIQUE 去不掉），所以这四张表要重建；重建期间关闭外键检查，编号保持不变。
-- 升级前已有的数据归编号 1 的账号，也就是升级后创建的第一个管理员。

-- 账号。编号不重复使用（AUTOINCREMENT），删除账号后新账号不会接手它的编号。
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    last_login_at INTEGER
);

-- 页面会话：库里只存会话令牌的 SHA-256。改密码、停用、删除账号时删掉对应的会话。
CREATE TABLE sessions (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE servers_new (
    id          INTEGER PRIMARY KEY,
    owner_id    INTEGER NOT NULL,
    name        TEXT    NOT NULL,
    title       TEXT    NOT NULL DEFAULT '',
    host        TEXT    NOT NULL,
    port        INTEGER NOT NULL DEFAULT 22,
    username    TEXT    NOT NULL DEFAULT '',
    key_path    TEXT    NOT NULL DEFAULT '',
    ssh_options TEXT    NOT NULL DEFAULT '[]',
    facts       TEXT,
    checked_at  INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (owner_id, name)
);
INSERT INTO servers_new (id, owner_id, name, title, host, port, username, key_path, ssh_options, facts, checked_at, created_at, updated_at)
    SELECT id, 1, name, title, host, port, username, key_path, ssh_options, facts, checked_at, created_at, updated_at FROM servers;
DROP TABLE servers;
ALTER TABLE servers_new RENAME TO servers;

CREATE TABLE secrets_new (
    id          INTEGER PRIMARY KEY,
    owner_id    INTEGER NOT NULL,
    name        TEXT    NOT NULL,
    value_enc   TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (owner_id, name)
);
INSERT INTO secrets_new (id, owner_id, name, value_enc, description, created_at, updated_at)
    SELECT id, 1, name, value_enc, description, created_at, updated_at FROM secrets;
DROP TABLE secrets;
ALTER TABLE secrets_new RENAME TO secrets;

CREATE TABLE projects_new (
    id          INTEGER PRIMARY KEY,
    owner_id    INTEGER NOT NULL,
    name        TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (owner_id, name)
);
INSERT INTO projects_new (id, owner_id, name, title, description, created_at, updated_at)
    SELECT id, 1, name, title, description, created_at, updated_at FROM projects;
DROP TABLE projects;
ALTER TABLE projects_new RENAME TO projects;

CREATE TABLE credentials_new (
    id          INTEGER PRIMARY KEY,
    owner_id    INTEGER NOT NULL,
    name        TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    title       TEXT    NOT NULL DEFAULT '',
    fields      TEXT    NOT NULL DEFAULT '{}',
    secrets_enc TEXT    NOT NULL DEFAULT '',
    info        TEXT,
    checked_at  INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (owner_id, name)
);
INSERT INTO credentials_new (id, owner_id, name, kind, title, fields, secrets_enc, info, checked_at, created_at, updated_at)
    SELECT id, 1, name, kind, title, fields, secrets_enc, info, checked_at, created_at, updated_at FROM credentials;
DROP TABLE credentials;
ALTER TABLE credentials_new RENAME TO credentials;

ALTER TABLE jobs ADD COLUMN owner_id INTEGER NOT NULL DEFAULT 0;
UPDATE jobs SET owner_id = 1;
CREATE INDEX jobs_owner ON jobs (owner_id, id);
