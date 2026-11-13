// Package skills 嵌入给 AI agent 用的 Skill，供 `easel skill` 打印和安装。
package skills

import _ "embed"

// Easel 是 skills/easel/SKILL.md 的内容。
//
//go:embed easel/SKILL.md
var Easel []byte
