-- +goose Up

-- 端口代理的上游：serve_kind = 'proxy' 的原型各有一行。目标只能是服务器本机回环地址上的端口。
CREATE TABLE upstream (
    prototype_id  INTEGER PRIMARY KEY REFERENCES prototype (id) ON DELETE CASCADE,
    port          INTEGER NOT NULL UNIQUE CHECK (port BETWEEN 1 AND 65535),
    -- localhost：对上游伪装成 localhost:<port>（默认）；preserve：保留公网 Host
    host_mode     TEXT    NOT NULL DEFAULT 'localhost' CHECK (host_mode IN ('localhost', 'preserve')),
    -- 注册时监听这个端口的进程（Linux 下由 /proc 得到；拿不到时为空，不做检查）
    process_exe   TEXT    NOT NULL DEFAULT '',
    process_cwd   TEXT    NOT NULL DEFAULT '',
    registered_at INTEGER NOT NULL,
    last_seen_at  INTEGER, -- 最近一次拨通的时间
    -- 非空表示分享已暂停：process_changed | unreachable
    paused_reason TEXT    NOT NULL DEFAULT '',
    paused_at     INTEGER
);

-- +goose Down
DROP TABLE upstream;
