-- 任务：一次部署或回滚，可以包含多个应用。
CREATE TABLE jobs (
    id          INTEGER PRIMARY KEY,
    kind        TEXT    NOT NULL,              -- deploy / rollback
    status      TEXT    NOT NULL,              -- queued / running / success / failed / rolled_back / canceled
    initiator   TEXT    NOT NULL,              -- 发起方：page，以后有 token:<名称>
    error       TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    started_at  INTEGER,
    finished_at INTEGER
);

-- 任务里的每个应用。应用删除后记录保留（app_id 置空），项目、应用名称冗余保存。
CREATE TABLE job_units (
    id          INTEGER PRIMARY KEY,
    job_id      INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,              -- 在任务里的顺序，从 0 开始
    app_id      INTEGER REFERENCES apps (id) ON DELETE SET NULL,
    project     TEXT    NOT NULL,
    app         TEXT    NOT NULL,
    title       TEXT    NOT NULL,
    driver      TEXT    NOT NULL,
    server      TEXT    NOT NULL,
    revision    INTEGER NOT NULL,              -- 使用的应用配置版本
    target      TEXT    NOT NULL DEFAULT '',   -- 回滚的目标版本
    version     TEXT    NOT NULL DEFAULT '',   -- 部署的新版本（回滚时等于 target）
    previous    TEXT    NOT NULL DEFAULT '',   -- 执行前的线上版本
    commit_hash TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL,              -- pending / running / success / failed / rolled_back / canceled / skipped
    step        TEXT    NOT NULL DEFAULT '',   -- 正在执行或失败的步骤
    error       TEXT    NOT NULL DEFAULT '',
    started_at  INTEGER,
    finished_at INTEGER,
    UNIQUE (job_id, seq)
);
CREATE INDEX job_units_app ON job_units (app_id, id);

-- 任务事件：步骤开始、完成、失败，命令输出，提示。页面刷新后从这里补齐日志。
CREATE TABLE job_events (
    id       INTEGER PRIMARY KEY,
    job_id   INTEGER NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    unit_seq INTEGER,                          -- 为空表示任务级
    time     INTEGER NOT NULL,                 -- Unix 毫秒
    type     TEXT    NOT NULL,                 -- step_start / step_done / step_fail / output / info / warn / error
    step     TEXT    NOT NULL DEFAULT '',
    message  TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX job_events_job ON job_events (job_id, id);
