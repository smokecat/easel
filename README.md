# easel

自托管的原型预览与分享工具。一条命令推送静态页面、站点、图片、录屏或 Markdown，或者暴露本机正在运行的端口，就能得到一个预览链接。分享链接可以随时撤销，也可以设置密码和有效期。

面向单管理员的自托管场景，运行在 Linux / macOS 上。服务端和 CLI 是同一个二进制。

> 状态：早期开发中，目前只有项目骨架。

## 从源码构建

需要 Go 1.26+、[Bun](https://bun.sh) 1.4+、make。

```sh
make build          # 构建管理面板并编译 bin/easel
./bin/easel version
./bin/easel serve   # 默认监听 127.0.0.1:7780
```

easel 不内置 TLS，只监听本机端口，对外由 Caddy 等反向代理提供。

## 开发

```sh
make dev    # 后端（:7780）+ Vite dev server（转发 /api 到后端）
make test   # Go 与前端测试
make lint   # gofmt、go vet、oxlint、tsc
```

## 许可证

[MIT](LICENSE)
