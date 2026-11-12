# easel

自托管的原型预览与分享工具。一条命令推送静态页面、站点、图片、录屏或 Markdown，或者暴露本机正在运行的端口，就能得到一个预览链接。分享链接可以随时撤销，也可以设置密码和有效期。

面向单管理员的自托管场景，运行在 Linux / macOS 上。服务端和 CLI 是同一个二进制。

> 状态：早期开发中。已能推送静态目录、按内容主机名预览，预览需要登录或分享链接；端口代理、管理面板还没做。

## 从源码构建

需要 Go 1.26+、[Bun](https://bun.sh) 1.4+、make。

```sh
make build          # 构建管理面板并编译 bin/easel
./bin/easel version
./bin/easel serve   # 默认监听 127.0.0.1:7780
```

easel 不内置 TLS，只监听本机端口，对外由 Caddy 等反向代理提供。

### 作为 systemd 用户服务运行（Linux）

```sh
make install                                   # 装到 ~/.local/bin/easel
install -Dm644 deploy/systemd/easel.service ~/.config/systemd/user/easel.service
systemctl --user daemon-reload
systemctl --user enable --now easel
loginctl enable-linger "$USER"                 # 不登录也常驻
journalctl --user -u easel -f                  # 看日志
```

升级：`make install && systemctl --user restart easel`。要改监听地址或公开地址时用 `systemctl --user edit easel` 写 drop-in 覆盖 `ExecStart`。

### 接到公网

后台和原型内容必须在不同主机上：后台用 `easel.example.com`，原型用 `*.easel.example.com`（两条 DNS 记录），启动参数 `--app-url https://easel.example.com --content-url https://easel.example.com`。Caddy 配置见 [`deploy/caddy/Caddyfile.example`](deploy/caddy/Caddyfile.example)：证书按需签发，easel 的 `/_tls/ask` 只放行后台主机和已存在的原型。

> 后台和内容同在一个注册域下（如上例）时，原型和后台「同站」：原型脚本能往父域种 cookie。easel 自己的 cookie 都是 host-only 的 `__Host-` cookie，后台会话不会被原型读到，但这种部署形态只适合内容基本可信的场景（自己或自己的 agent 产出的原型）。要放不可信的内容，给内容另用一个注册域。

## 登录与访问控制

```sh
easel admin passwd            # 在服务端所在的机器上设置管理员密码（经 unix socket）
```

- 预览链接需要访问权：没有时浏览器会被送到后台登录，登录后自动回到原页面。每个原型主机各自换一张只对它有效的访问 cookie，后台的会话 cookie 不会发给内容主机。
- 后台会话 30 天有效；改密码会注销所有会话。登录失败过多会临时锁定（全局计数），此时仍可以在服务端机器上用 CLI。
- 服务端第一次启动时在数据目录生成 `secret.key`，用来派生分享链接。换掉或丢失它，所有分享链接都会失效；备份数据目录时一起备份。

### 分享链接

```sh
easel share create myapp                       # stdout 输出分享链接
easel share create myapp --expires 7d --note "给 Bob"
easel share create myapp --password            # 终端里提示输入访客密码；非交互用 --password-stdin
easel share list [myapp]                       # 链接随时可以再复制；--all 包括已过期、已撤销的
easel share revoke <id>                        # 立即生效，已经打开的访客下一个请求就会失效
```

分享链接授予原型的主地址（跟随最新版本）。访客打开链接后换成本主机的访问 cookie（24 小时，且不超过分享的有效期），地址栏里不再带令牌。密码错误按链接计数限速。

### 在别的机器上用 CLI

```sh
# 在服务端机器上创建 token（明文只显示一次；默认权限 read,write，需要管理分享链接时加 share）
easel token create --name laptop --scope read,write,share --expires 90d

# 在另一台机器上
easel login --server https://easel.example.com   # 粘贴 token；非交互用 --token-stdin
easel push ./dist
```

- 连接的选择顺序：`--server` / `EASEL_SERVER`（token 取 `EASEL_TOKEN` 或已保存的凭据）> 本机 unix socket > `easel login` 保存的服务端。凭据存在 `~/.config/easel/credentials.toml`（0600）。
- `easel token list`、`easel token revoke <id>` 只能在服务端机器上（或用后台会话）执行，token 不能管理 token。`easel logout` 只删除本地凭据，不撤销 token。

## 推送

```sh
easel serve &                 # 同机的 CLI 经 unix socket 连接，免登录
easel push ./dist             # stdout 输出预览链接，如 http://myapp-k3x9qa.localhost:7780/（打开需要登录）
easel push ./dist --json      # 结构化结果，字段只增不改
```

- 原型名按 `--name` > `EASEL_NAME` > `easel.toml` > git 仓库名 + 相对路径 推断，不写任何状态文件。推断出的名字撞上来源不同的原型时拒绝（退出码 5），加 `--name` 即可。
- 再推一次得到新版本，只上传变化的文件；内容完全一样时不产生新版本。
- 默认排除点文件（`.well-known/` 除外）、`node_modules/` 和常见密钥文件；不读 `.gitignore`。`.easelignore`、`--exclude` 追加规则，`--include` 加回来，`--dry-run` 只列出会上传什么。
- 每个原型在内容域的一级子域上提供，`<label>--v<N>` 固定到某个版本。默认内容域是 `localhost:<端口>`（浏览器会把 `*.localhost` 解析到本机）；部署时用 `--app-url`、`--content-url` 设置公开地址。
- 也可以不用 CLI，直接把 tar / tar.gz / zip 发到整包端点：`tar -C dist -cf - . | curl --unix-socket <socket> --data-binary @- 'http://easel/api/v1/push?name=myapp'`

`easel.toml`（可选，放在项目根）：

```toml
name = "myapp"
dir = "dist"       # easel push 不带参数时推这个目录
spa = true         # 未命中的页面路径回退到入口页
ignore = ["*.map"]
```

## 开发

```sh
make dev    # 后端（:7780）+ Vite dev server（转发 /api 到后端）
make test   # Go 与前端测试
make lint   # gofmt、go vet、oxlint、tsc
```

## 许可证

[MIT](LICENSE)
