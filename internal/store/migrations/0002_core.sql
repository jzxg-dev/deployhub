-- 服务器：名称创建后不能改，应用参数里用名称引用。
CREATE TABLE servers (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    title       TEXT    NOT NULL DEFAULT '',
    host        TEXT    NOT NULL,
    port        INTEGER NOT NULL DEFAULT 22,
    username    TEXT    NOT NULL DEFAULT '',
    key_path    TEXT    NOT NULL DEFAULT '',
    ssh_options TEXT    NOT NULL DEFAULT '[]', -- JSON 数组
    facts       TEXT,                          -- 最近一次检测结果（JSON），没检测过为 NULL
    checked_at  INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- 密钥与变量：值加密保存（vault），参数里用 ${名称} 引用。
CREATE TABLE secrets (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    value_enc   TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE projects (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    title       TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- 应用：通用字段（名称、服务器等）在列里冗余一份，便于查询；完整参数在 params（JSON，敏感字段已加密）。
CREATE TABLE apps (
    id          INTEGER PRIMARY KEY,
    project_id  INTEGER NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    name        TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    driver      TEXT    NOT NULL,
    server_id   INTEGER REFERENCES servers (id) ON DELETE RESTRICT,
    params      TEXT    NOT NULL,
    revision    INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (project_id, name)
);

-- 应用每次保存生成一个版本，用于查看修改记录和恢复。
CREATE TABLE app_revisions (
    id          INTEGER PRIMARY KEY,
    app_id      INTEGER NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    revision    INTEGER NOT NULL,
    params      TEXT    NOT NULL,
    created_at  INTEGER NOT NULL,
    UNIQUE (app_id, revision)
);
