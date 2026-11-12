# easel

自托管的原型预览与分享工具。一条命令推送静态页面、站点、图片、录屏或 Markdown，或者暴露本机正在运行的端口，就能得到一个预览链接。分享链接可以随时撤销，也可以设置密码和有效期。

面向单管理员的自托管场景，运行在 Linux / macOS 上。服务端和 CLI 是同一个二进制。

> 状态：早期开发中。已能推送静态目录并按内容主机名预览；登录、分享链接、端口代理还没做。

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

## 推送

```sh
easel serve &                 # 同机的 CLI 经 unix socket 连接，免登录
easel push ./dist             # stdout 输出预览链接，如 http://myapp-k3x9qa.localhost:7780/
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

> 注意：访问控制还没实现，目前写 API 只开放在本机 unix socket 上，但**预览链接不需要登录**，能访问内容主机的人都能打开。在实现登录和分享链接之前，不要把内容域暴露到公网。

## 开发

```sh
make dev    # 后端（:7780）+ Vite dev server（转发 /api 到后端）
make test   # Go 与前端测试
make lint   # gofmt、go vet、oxlint、tsc
```

## 许可证

[MIT](LICENSE)
