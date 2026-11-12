package api

import (
	"net/http"

	"github.com/smokecat/easel/internal/publish"
)

// PrototypeInfo 是一个原型。
type PrototypeInfo struct {
	Name      string `json:"name"`
	ServeKind string `json:"serve_kind"` // static | proxy
	Title     string `json:"title"`
	// PreviewURL 跟随最新版本。
	PreviewURL string `json:"preview_url"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	// Current 是当前版本；还没有版本时为 null。
	Current  *VersionInfo `json:"current"`
	Versions int64        `json:"versions"`
}

// VersionInfo 是一个版本的概要。
type VersionInfo struct {
	Number    int64  `json:"number"`
	Kind      string `json:"kind"` // site | assets
	Note      string `json:"note"`
	Files     int64  `json:"files"`
	Bytes     int64  `json:"bytes"`
	CreatedAt string `json:"created_at"`
	URL       string `json:"url"` // 固定到这个版本
}

// PrototypeList 是 GET /api/v1/prototypes 的响应。
type PrototypeList struct {
	Prototypes []PrototypeInfo `json:"prototypes"`
}

func (a *API) listPrototypes(w http.ResponseWriter, r *http.Request) {
	ps, err := a.Publish.List(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	resp := PrototypeList{Prototypes: make([]PrototypeInfo, len(ps))}
	for i, p := range ps {
		resp.Prototypes[i] = prototypeInfo(p)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) getPrototype(w http.ResponseWriter, r *http.Request) {
	p, err := a.Publish.Get(r.Context(), r.PathValue("name"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prototypeInfo(*p))
}

func (a *API) deletePrototype(w http.ResponseWriter, r *http.Request) {
	if err := a.Publish.Delete(r.Context(), r.PathValue("name")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func prototypeInfo(p publish.Prototype) PrototypeInfo {
	info := PrototypeInfo{
		Name: p.Name, ServeKind: p.ServeKind, Title: p.Title, PreviewURL: p.PreviewURL,
		CreatedAt: rfc3339(p.CreatedAt), UpdatedAt: rfc3339(p.UpdatedAt), Versions: p.Versions,
	}
	if c := p.Current; c != nil {
		info.Current = &VersionInfo{
			Number: c.Number, Kind: c.Kind, Note: c.Note, Files: c.Files, Bytes: c.Bytes,
			CreatedAt: rfc3339(c.CreatedAt), URL: c.URL,
		}
	}
	return info
}
