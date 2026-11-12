package cli

import (
	"errors"
	"net/http"
	"strings"

	"github.com/smokecat/easel/internal/client"
	"github.com/smokecat/easel/internal/publish"
)

// failure 是归类后的错误：退出码 + `--json` 里的 error 对象。
type failure struct {
	exit      int
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	Retryable bool   `json:"retryable"`
}

func classify(err error) failure {
	var ue *usageError
	var ce *client.ConnError
	var ae *client.APIError
	var auErr *authError
	switch {
	case errors.As(err, &ue):
		return failure{exit: ExitUsage, Code: "usage", Message: ue.msg, Hint: ue.hint}
	case errors.As(err, &auErr):
		return failure{exit: ExitAuth, Code: "unauthorized", Message: auErr.msg, Hint: auErr.hint}
	case errors.As(err, &ce):
		hint := "start the server with `easel serve` on this machine, or point --socket / EASEL_SOCKET at its socket"
		if strings.Contains(ce.Target, "://") {
			hint = "check the server URL and your network; the saved server comes from `easel login`"
		}
		return failure{exit: ExitUnreachable, Code: "unreachable", Message: ce.Error(), Retryable: true, Hint: hint}
	case errors.As(err, &ae):
		f := failure{exit: ExitError, Code: ae.Body.Code, Message: ae.Body.Message, Hint: ae.Body.Hint}
		switch {
		case ae.Body.Code == publish.CodeUnauthorized || ae.Status == http.StatusForbidden:
			f.exit = ExitAuth
		case ae.Body.Code == publish.CodeNotFound:
			f.exit = ExitNotFound
		case ae.Body.Code == publish.CodeNameConflict:
			f.exit = ExitConflict
		case ae.Body.Code == publish.CodeTooLarge:
			f.exit = ExitRejected
		case ae.Body.Code == publish.CodeInvalidName:
			f.exit = ExitUsage
		case ae.Status >= 500:
			f.exit, f.Retryable = ExitServer, true
		}
		return f
	default:
		return failure{exit: ExitError, Code: "error", Message: err.Error()}
	}
}
