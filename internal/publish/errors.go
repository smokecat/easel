package publish

import (
	"errors"
	"fmt"
	"net/http"
)

// 错误码。API 在 JSON 里返回，CLI 据此决定退出码；只增不改。
const (
	CodeInvalidRequest = "invalid_request"
	CodeInvalidName    = "invalid_name"
	CodeNameConflict   = "name_conflict"
	CodeNotFound       = "not_found"
	CodeTooLarge       = "too_large"
	CodeMissingBlobs   = "missing_blobs"
	CodeUnauthorized   = "unauthorized"
	CodeInternal       = "internal"
)

// Error 是带 HTTP 状态和错误码的错误，API 原样序列化给客户端。
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func (e *Error) Error() string {
	if e.Hint != "" {
		return e.Message + "（" + e.Hint + "）"
	}
	return e.Message
}

func errf(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func (e *Error) withHint(h string) *Error { e.Hint = h; return e }

// AsError 把任意错误转成 *Error；不是 *Error 的当作内部错误，不泄漏细节。
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "internal server error"}
}
