---
name: easel
description: 把静态页面、构建产物、图片或录屏推送到自托管的 easel，拿到可在浏览器打开的预览链接。用户要「预览一下」「给我个链接」「发布这个原型 / 页面」，或者你生成了 HTML / 站点想让用户看效果时使用。
---

# easel：推送原型，拿预览链接

## 前提

- 本机有 `easel` 命令，且同一台机器上跑着 `easel serve`（CLI 经本机 unix socket 连接，不需要登录）。
- 先跑 `easel version` 确认命令存在；推送时报「cannot reach easel」（退出码 7）说明服务端没起来，告诉用户，不要自己去起长期运行的服务。

## 推送

```sh
easel push <目录或文件> --json
```

- 目录里有 `index.html` 就是站点；根目录只有一个 HTML 时以它为入口；否则当作素材集（图片、视频会列成一个浏览页）。
- 推构建产物时推输出目录（`dist`、`build`、`out` 等），不要推源码根目录。
- 同一个来源再推一次就是新版本，只上传变化的文件；内容没变时 `version.unchanged` 为 true，不会产生新版本。
- 加 `-m "<备注>"` 记下这一版改了什么。

## 读结果

`--json` 时 stdout 是一个 JSON 对象，成功和失败都是：

- `urls.preview`：预览链接，跟随最新版本，**把它给用户**。
- `urls.version`：固定到这一版的链接，内容不会再变。
- `version.number`、`version.unchanged`
- `access.preview_requires_login`：目前为 false，预览链接没有访问控制。
- `stats.skipped`：被默认规则排除的文件（点文件、`node_modules/`、密钥文件）。如果用户确实需要其中某个文件，加 `--include <模式>`。
- 失败时 `ok` 为 false，看 `error.code`、`error.message`、`error.hint`。

## 原型名

缺省按 git 仓库名（加相对路径）推断。退出码 5（`name_conflict`）表示推断出的名字已被别的来源占用：换一个 `--name <slug>`，或在确认是同一个原型后用 `--name` 指定它。名字只能用小写字母、数字和单个短横线。

## 退出码

0 成功（含 unchanged）、2 参数或路径不对、5 名字冲突、6 超过大小或文件数上限、7 连不上服务端、8 服务端错误。退出码 2、5、6 改参数后可以重试；7、8 稍后重试或告诉用户。

## 不要做

- 不要推送 `$HOME`、`/` 或包含密钥的目录；不要用 `--include` 把 `.env` 之类加回来。
- 非交互环境里 easel 不会提示输入，缺信息直接失败并说明该加哪个参数，按提示补参数即可。
