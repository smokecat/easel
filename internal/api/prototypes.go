package api

import (
	"net/http"

	"github.com/smokecat/easel/internal/proxy"
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
	// Proxy 只对端口代理原型有值。
	Proxy *ProxyInfo `json:"proxy"`
}

// ProxyInfo 是端口代理的状态。
type ProxyInfo struct {
	Port     int    `json:"port"`
	HostMode string `json:"host_mode"` // localhost | preserve
	// Status 是 active 或 paused（分享暂停，管理员仍可访问）。
	Status       string  `json:"status"`
	PausedReason string  `json:"paused_reason,omitempty"` // process_changed | unreachable
	LastSeenAt   *string `json:"last_seen_at"`            // 最近一次检测到端口可达
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

func (a *API) expose(w http.ResponseWriter, r *http.Request) {
	var req publish.ExposeRequest
	if !a.decode(w, r, &req) {
		return
	}
	var listener publish.Listener
	reachable := req.Port >= 1 && req.Port <= 65535 && proxy.Reachable(r.Context(), req.Port)
	if reachable {
		if p, ok := proxy.ListenerProcess(req.Port); ok {
			listener = publish.Listener{Exe: p.Exe, Cwd: p.Cwd}
		}
	}
	res, err := a.Publish.Expose(r.Context(), r.PathValue("name"), req, reachable, listener)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if res.Prototype.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, res)
}

func prototypeInfo(p publish.Prototype) PrototypeInfo {
	info := PrototypeInfo{
		Name: p.Name, ServeKind: p.ServeKind, Title: p.Title, PreviewURL: p.PreviewURL,
		CreatedAt: rfc3339(p.CreatedAt), UpdatedAt: rfc3339(p.UpdatedAt), Versions: p.Versions,
	}
	if u := p.Upstream; u != nil {
		info.Proxy = &ProxyInfo{Port: u.Port, HostMode: u.HostMode, Status: "active", PausedReason: u.PausedReason, LastSeenAt: optTime(u.LastSeenAt)}
		if u.PausedReason != "" {
			info.Proxy.Status = "paused"
		}
	}
	if c := p.Current; c != nil {
		info.Current = &VersionInfo{
			Number: c.Number, Kind: c.Kind, Note: c.Note, Files: c.Files, Bytes: c.Bytes,
			CreatedAt: rfc3339(c.CreatedAt), URL: c.URL,
		}
	}
	return info
}
