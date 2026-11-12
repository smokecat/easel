// Package publish 实现推送流程：manifest 三步协议、整包上传、忽略规则，以及服务端和 CLI 共用的协议类型。
//
// 三步协议：
//  1. POST /api/v1/prototypes/{name}/versions，带文件清单 → 回 upload_id 和缺失的哈希（内容未变时回 unchanged）
//  2. PUT /api/v1/uploads/{id}/blobs，tar 流，条目名是哈希，只含缺失的文件（可 gzip）
//  3. POST /api/v1/uploads/{id}/commit → 原子地生成新版本，回预览 URL
//
// 整包端点 POST /api/v1/push?name=… 接受 tar / tar.gz / zip，给 curl 和网页拖拽用。
package publish

// FileEntry 是 manifest 里的一个文件。Path 不带前导斜杠，用 / 分隔。
type FileEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Source 描述推送来源，用来拒绝「推断出的名字撞上别的来源」。
type Source struct {
	// Fingerprint 是 git 远端或主机名 + 绝对路径，再加相对路径。
	Fingerprint string `json:"fingerprint,omitempty"`
	// Inferred 为 true 表示原型名是 CLI 推断的（不是 --name 或 easel.toml 给的）。
	Inferred bool `json:"inferred,omitempty"`
}

// CreateRequest 是第 1 步的请求体。
type CreateRequest struct {
	Files  []FileEntry `json:"files"`
	Note   string      `json:"note,omitempty"`
	SPA    bool        `json:"spa,omitempty"`
	Source Source      `json:"source"`
}

// CreateResponse 是第 1 步的响应。Unchanged 时 Result 有值，不需要后两步。
type CreateResponse struct {
	Unchanged bool     `json:"unchanged"`
	UploadID  string   `json:"upload_id,omitempty"`
	Missing   []string `json:"missing"`
	// MissingBytes 是缺失文件的总大小，供 CLI 显示进度。
	MissingBytes int64   `json:"missing_bytes"`
	Result       *Result `json:"result,omitempty"`
}

// BlobsResponse 是第 2 步的响应。
type BlobsResponse struct {
	Received      int   `json:"received"`
	ReceivedBytes int64 `json:"received_bytes"`
}

// Result 是一次推送的结果，CLI 的 `--json` 输出在此基础上补充本地信息。字段只增不改。
type Result struct {
	Prototype ResultPrototype `json:"prototype"`
	Version   ResultVersion   `json:"version"`
	URLs      ResultURLs      `json:"urls"`
	Access    ResultAccess    `json:"access"`
	Stats     ResultStats     `json:"stats"`
	Warnings  []string        `json:"warnings"`
}

type ResultPrototype struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"` // site | assets
	Created bool   `json:"created"`
}

type ResultVersion struct {
	Number    int64  `json:"number"`
	Unchanged bool   `json:"unchanged"`
	Note      string `json:"note"`
}

type ResultURLs struct {
	// Preview 跟随最新版本。
	Preview string `json:"preview"`
	// Version 固定指向这个版本，内容永不变。
	Version   string  `json:"version"`
	Dashboard *string `json:"dashboard"`
	Share     *string `json:"share"`
}

type ResultAccess struct {
	PreviewRequiresLogin bool `json:"preview_requires_login"`
}

type ResultStats struct {
	Files         int      `json:"files"`
	Bytes         int64    `json:"bytes"`
	UploadedFiles int      `json:"uploaded_files"`
	UploadedBytes int64    `json:"uploaded_bytes"`
	Skipped       []string `json:"skipped"`
}

// ErrorBody 是 API 出错时的响应体。
type ErrorBody struct {
	Error *Error `json:"error"`
}
