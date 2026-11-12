-- +goose Up

-- 管理员：单管理员，固定一行（id = 1）。password_hash 是 Argon2id 的 PHC 字符串。
CREATE TABLE admin (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash TEXT    NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- 后台会话。id_hash 是 cookie 值的 SHA-256（hex），明文只在浏览器里。
CREATE TABLE session (
    id_hash    TEXT    PRIMARY KEY,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    user_agent TEXT    NOT NULL DEFAULT ''
) WITHOUT ROWID;

-- API token：只存哈希，明文只在创建时显示一次。scopes 是逗号分隔的 read / write / share。
CREATE TABLE api_token (
    id           INTEGER PRIMARY KEY,
    name         TEXT    NOT NULL,
    token_hash   TEXT    NOT NULL UNIQUE,
    -- 明文的前几位，列表里用来辨认，不足以还原
    hint         TEXT    NOT NULL,
    scopes       TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER, -- NULL 表示不过期
    last_used_at INTEGER,
    revoked_at   INTEGER
);

-- 分享链接。令牌 = HMAC(服务端密钥, nonce)，所以后台能再次显示；token_hash 用来按令牌查。
CREATE TABLE share (
    id            INTEGER PRIMARY KEY,
    prototype_id  INTEGER NOT NULL REFERENCES prototype (id) ON DELETE CASCADE,
    nonce         TEXT    NOT NULL,
    token_hash    TEXT    NOT NULL UNIQUE,
    note          TEXT    NOT NULL DEFAULT '',
    password_hash TEXT    NOT NULL DEFAULT '', -- 空串表示不需要密码
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER, -- NULL 表示不过期
    revoked_at    INTEGER
);

CREATE INDEX share_prototype ON share (prototype_id);

-- 跨主机的一次性票据：后台签发，内容主机核销后换成 view_session。
CREATE TABLE ticket (
    id_hash      TEXT    PRIMARY KEY,
    host_label   TEXT    NOT NULL, -- 只能在这个内容主机上核销
    session_hash TEXT    NOT NULL REFERENCES session (id_hash) ON DELETE CASCADE,
    expires_at   INTEGER NOT NULL
) WITHOUT ROWID;

-- 内容主机上的访问会话：只对一个主机有效。管理员的跟随后台会话，访客的跟随分享链接。
CREATE TABLE view_session (
    id_hash      TEXT    PRIMARY KEY,
    host_label   TEXT    NOT NULL,
    session_hash TEXT    REFERENCES session (id_hash) ON DELETE CASCADE,
    share_id     INTEGER REFERENCES share (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    CHECK ((session_hash IS NULL) <> (share_id IS NULL))
) WITHOUT ROWID;

-- +goose Down
DROP TABLE view_session;
DROP TABLE ticket;
DROP TABLE share;
DROP TABLE api_token;
DROP TABLE session;
DROP TABLE admin;
