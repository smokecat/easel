-- +goose Up

-- 原型：一个可预览的东西。serve_kind 是「怎么提供内容」这条轴（静态版本 / 端口代理）；
-- 端口代理的上游登记以后加 upstream 表，二期的构建、进程托管也只加表。
CREATE TABLE prototype (
    id                  INTEGER PRIMARY KEY,
    name                TEXT    NOT NULL UNIQUE,
    -- 内容主机的一级标签：<name>-<随机后缀>，主机名会进 DNS / CT 日志，不当秘密用
    label               TEXT    NOT NULL UNIQUE,
    title               TEXT    NOT NULL DEFAULT '',
    serve_kind          TEXT    NOT NULL DEFAULT 'static' CHECK (serve_kind IN ('static', 'proxy')),
    -- 首次推送时的来源指纹（git 远端或主机名 + 路径），用来拒绝推断撞名
    source_fingerprint  TEXT    NOT NULL DEFAULT '',
    current_revision_id INTEGER REFERENCES revision (id),
    created_at          INTEGER NOT NULL, -- Unix 毫秒
    updated_at          INTEGER NOT NULL
);

-- 版本：不可变。origin 是「内容怎么来」这条轴（推送 / 回滚 / 二期的服务端构建）。
CREATE TABLE revision (
    id            INTEGER PRIMARY KEY,
    prototype_id  INTEGER NOT NULL REFERENCES prototype (id) ON DELETE CASCADE,
    number        INTEGER NOT NULL,
    origin        TEXT    NOT NULL CHECK (origin IN ('upload', 'rollback', 'build')),
    kind          TEXT    NOT NULL CHECK (kind IN ('site', 'assets')),
    -- 入口文件（manifest 内的相对路径）；素材集为空
    entry         TEXT    NOT NULL DEFAULT '',
    spa           INTEGER NOT NULL DEFAULT 0,
    note          TEXT    NOT NULL DEFAULT '',
    -- manifest 的规范化哈希，内容和上一版完全一致时不产生新版本
    manifest_hash TEXT    NOT NULL,
    file_count    INTEGER NOT NULL,
    total_bytes   INTEGER NOT NULL,
    created_at    INTEGER NOT NULL,
    UNIQUE (prototype_id, number)
);

-- manifest：版本里的每个文件指向 blob 库里的一个哈希。
CREATE TABLE artifact (
    revision_id  INTEGER NOT NULL REFERENCES revision (id) ON DELETE CASCADE,
    path         TEXT    NOT NULL, -- 不带前导斜杠，如 assets/app.js
    blob_hash    TEXT    NOT NULL,
    size         INTEGER NOT NULL,
    content_type TEXT    NOT NULL,
    PRIMARY KEY (revision_id, path)
) WITHOUT ROWID;

CREATE INDEX artifact_blob_hash ON artifact (blob_hash);

-- 三步推送协议里尚未提交的上传会话。
CREATE TABLE upload (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    request    TEXT    NOT NULL, -- 第 1 步的请求体（JSON），提交时据此生成版本
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE upload;
DROP TABLE artifact;
DROP TABLE revision;
DROP TABLE prototype;
