# easel

自托管的 Web 工具，用来上传、管理、预览和分享原型：静态页面与站点、图片/录屏/Markdown 素材，以及本机正在运行的端口。一条命令推送，得到预览链接；分享链接可撤销，可设密码和有效期。

面向单管理员自托管，部署在 Linux / macOS。许可证 MIT。

## 技术栈

- 后端：Go，单二进制。服务端和 CLI 是同一个可执行文件（`easel serve`、`easel push` …）
- 数据：SQLite（`modernc.org/sqlite`，不依赖 cgo）；迁移用 goose，查询用 sqlc 生成
- 文件：按 SHA-256 去重的 blob 库，每个版本一份 manifest
- 管理面板：React + Vite + TypeScript + Tailwind + shadcn/ui，构建产物用 `go:embed` 嵌入二进制
- 包管理：前端用 bun

## 目录

| 路径 | 职责 |
|---|---|
| `cmd/easel/` | 唯一入口，只做参数解析和装配 |
| `internal/cli/` | 子命令实现、输出格式、退出码 |
| `internal/client/` | CLI 调服务端的 HTTP 客户端 |
| `internal/config/` | 服务端配置、CLI 凭据、`easel.toml` |
| `internal/server/` | HTTP 装配，按主机分流到后台或内容子域 |
| `internal/api/` | 后台与 CLI 用的 HTTP API |
| `internal/content/` | 提供静态版本和素材 |
| `internal/proxy/` | 端口代理 |
| `internal/auth/` | 会话、API token、分享链接、限速 |
| `internal/store/` | SQLite 迁移与查询 |
| `internal/blob/` | 内容寻址文件库、manifest、垃圾回收 |
| `internal/publish/` | 推送协议、整包上传、忽略规则 |
| `internal/thumb/` | 缩略图 |
| `web/` | 管理面板源码 |
| `skills/easel/` | 给 AI agent 用的 Skill |
| `deploy/` | Caddyfile 示例、systemd unit |
| `docs/` | 部署、安全模型、CLI 参考、HTTP API |

## 必须守住的边界

- 原型内容一律视为不可信。后台和原型内容在不同主机上提供；后台的会话 cookie 不得对内容主机可见。
- `internal/content/` 和 `internal/proxy/` 只服务内容主机，`internal/api/` 只服务后台主机，两边不共用 cookie 逻辑。
- 令牌、密码、会话只在 `internal/auth/` 里处理。API token 只存哈希；密码用 Argon2id。
- `internal/blob/` 只管哈希到内容，不知道原型和版本；元数据归 `internal/store/`。
- 端口代理的目标默认只允许回环地址。
- 已发布的版本不可变；回滚是新建一个指向旧内容的版本。

## CLI 约定

- 结果写 stdout，进度和日志写 stderr；`--json` 的字段保持稳定。
- 非交互环境下不得出现提示，缺信息时以非零退出码失败，并说明该加哪个参数。

## 开发命令

需要 Go 1.26+、Bun（版本见 `web/package.json` 的 `packageManager`）、make。

```
make deps     # bun install --frozen-lockfile + go mod download
make dev      # 后端 easel serve（127.0.0.1:7780）+ Vite dev server（转发 /api、/healthz）
make build    # 构建前端到 web/dist，再编译 bin/easel（CGO_ENABLED=0，版本号由 ldflags 注入）
make go       # 只编译 Go，用现有的 web/dist
make test     # go test ./... 与 vitest
make lint     # gofmt 检查、go vet、oxlint、tsc
make fmt      # gofmt -w
make generate # 改了 internal/store 的迁移或 queries.sql 后重新生成 sqlc 代码（生成结果要提交）
```

- `web/dist/` 只提交 `.gitkeep`，没构建前端时 Go 也能编译测试，`easel serve` 会对首页返回 503 并提示 `make build`。vite build 会清空 dist，Makefile 在构建后补回 `.gitkeep`。
- 前端组件用 `bunx --bun shadcn@latest add <组件>` 添加（`web/` 下运行），生成的 `src/components/ui/` 不必手改风格。
- CLI 子命令目前用标准库 `flag` 分发（`internal/cli`），退出码：0 成功、1 运行错误、2 参数错误。
