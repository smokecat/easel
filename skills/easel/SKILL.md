---
name: easel
description: 把静态页面、构建产物、图片或录屏推送到自托管的 easel，拿到可在浏览器打开的预览链接。用户要「预览一下」「给我个链接」「发布这个原型 / 页面」，或者你生成了 HTML / 站点想让用户看效果时使用。
---

# easel：推送原型，拿预览链接

## 前提

- 本机有 `easel` 命令，并且满足其一：同一台机器上跑着 `easel serve`（经本机 unix socket，免登录）；或者用户已经 `easel login` 过远端服务端（也可以是环境变量 `EASEL_SERVER` + `EASEL_TOKEN`）。
- 先跑 `easel version` 确认命令存在；推送时报「cannot reach easel」（退出码 7）说明服务端没起来或连不上，退出码 3 说明没有凭据或权限不够。都告诉用户，不要自己去起长期运行的服务，也不要替用户创建或粘贴 token。

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
- `access.preview_requires_login`：为 true 时，预览链接只有登录了 easel 后台的人能打开（用户本人打开会先跳登录）。要给别人看，用下面的分享链接。
- `stats.skipped`：被默认规则排除的文件（点文件、`node_modules/`、密钥文件）。如果用户确实需要其中某个文件，加 `--include <模式>`。
- 失败时 `ok` 为 false，看 `error.code`、`error.message`、`error.hint`。

## 分享给别人

用户要「发给别人看」「给个能分享的链接」时：

```sh
easel share create <原型名> --json                 # url 字段就是分享链接
easel share create <原型名> --expires 7d --json    # 限定有效期
```

- 原型名就是推送结果里的 `prototype.name`。
- 要密码时用 `--password-stdin`，密码从 stdin 传入；密码由用户提供，不要自己编一个。
- `easel share list <原型名> --json` 能再次拿到已有的链接；`easel share revoke <id>` 撤销。
- 用 token 连远端时，token 需要 `share` 权限；退出码 3 且提示 share scope 时告诉用户。

## 查看与删除

- `easel list --json`：列出原型（`prototypes[].name`、`preview_url`、`current.number`）。推之前不确定名字时先看一下。
- `easel rm <原型名> --yes --json`：删除原型的所有版本和分享链接，不可恢复。**只在用户明确要求删除某个原型时执行**，不要为了「重来」或清理自己推错的东西擅自删除；推错了先告诉用户。

## 原型名

缺省按 git 仓库名（加相对路径）推断。退出码 5（`name_conflict`）表示推断出的名字已被别的来源占用：换一个 `--name <slug>`，或在确认是同一个原型后用 `--name` 指定它。名字只能用小写字母、数字和单个短横线。

## 退出码

0 成功（含 unchanged）、2 参数或路径不对、3 没有凭据或权限不够、4 原型或分享链接不存在、5 名字冲突、6 超过大小或文件数上限、7 连不上服务端、8 服务端错误。退出码 2、5、6 改参数后可以重试；7、8 稍后重试或告诉用户。

## 不要做

- 不要推送 `$HOME`、`/` 或包含密钥的目录；不要用 `--include` 把 `.env` 之类加回来。
- 非交互环境里 easel 不会提示输入，缺信息直接失败并说明该加哪个参数，按提示补参数即可。
