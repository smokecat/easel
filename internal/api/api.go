// Package api 是后台与 CLI 用的 HTTP API，只挂在后台主机上（以及本机 unix socket）。
package api

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/smokecat/easel/internal/auth"
	"github.com/smokecat/easel/internal/publish"
)

// maxManifestBytes 是第 1 步请求体的上限：两万个文件的清单大约 3 MB。
const maxManifestBytes = 16 << 20

// API 持有各处理器的依赖。
type API struct {
	Publish *publish.Service
	Log     *slog.Logger
	TempDir string
}

// Register 把 API 路由挂到 mux 上（路径都以 /api/v1/ 开头）。
func (a *API) Register(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/prototypes/{name}/versions", a.need(auth.ScopeWrite, a.createVersion))
	mux.Handle("PUT /api/v1/uploads/{id}/blobs", a.need(auth.ScopeWrite, a.putBlobs))
	mux.Handle("POST /api/v1/uploads/{id}/commit", a.need(auth.ScopeWrite, a.commit))
	mux.Handle("POST /api/v1/push", a.need(auth.ScopeWrite, a.pushArchive))
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.fail(w, r, &publish.Error{Status: http.StatusNotFound, Code: publish.CodeNotFound, Message: "no such API endpoint"})
	}))
}

func (a *API) need(scope auth.Scope, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.Allowed(r, scope) {
			a.fail(w, r, &publish.Error{
				Status: http.StatusUnauthorized, Code: publish.CodeUnauthorized,
				Message: "this API is only available over the local unix socket for now",
				Hint:    "run easel on the same machine as `easel serve`; token login is not implemented yet",
			})
			return
		}
		h(w, r)
	})
}

func (a *API) createVersion(w http.ResponseWriter, r *http.Request) {
	var req publish.CreateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxManifestBytes))
	if err := dec.Decode(&req); err != nil {
		a.fail(w, r, &publish.Error{Status: http.StatusBadRequest, Code: publish.CodeInvalidRequest, Message: "invalid JSON body: " + err.Error()})
		return
	}
	resp, err := a.Publish.Create(r.Context(), r.PathValue("name"), req)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	status := http.StatusCreated
	if resp.Unchanged {
		status = http.StatusOK
	}
	writeJSON(w, status, resp)
}

func (a *API) putBlobs(w http.ResponseWriter, r *http.Request) {
	body, err := decodeBody(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resp, err := a.Publish.PutBlobs(r.Context(), r.PathValue("id"), body)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) commit(w http.ResponseWriter, r *http.Request) {
	res, err := a.Publish.Commit(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) pushArchive(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	spa, _ := strconv.ParseBool(q.Get("spa"))
	res, err := a.Publish.PushArchive(r.Context(), q.Get("name"), r.Body, publish.ArchiveOptions{
		Note: q.Get("note"), SPA: spa, TempDir: a.TempDir,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// decodeBody 处理 Content-Encoding: gzip。
func decodeBody(r *http.Request) (io.Reader, error) {
	switch r.Header.Get("Content-Encoding") {
	case "", "identity":
		return r.Body, nil
	case "gzip":
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, &publish.Error{Status: http.StatusBadRequest, Code: publish.CodeInvalidRequest, Message: "invalid gzip body"}
		}
		return gz, nil
	default:
		return nil, &publish.Error{Status: http.StatusUnsupportedMediaType, Code: publish.CodeInvalidRequest, Message: "unsupported Content-Encoding"}
	}
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	e := publish.AsError(err)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		e = &publish.Error{Status: http.StatusRequestEntityTooLarge, Code: publish.CodeTooLarge, Message: "request body too large"}
	}
	if e.Code == publish.CodeInternal && a.Log != nil {
		a.Log.Error("api error", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeJSON(w, e.Status, publish.ErrorBody{Error: e})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
