package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/smokecat/easel/internal/auth"
	"github.com/smokecat/easel/internal/publish"
)

// maxDuration 防止 expires_in_seconds 溢出 time.Duration。
const maxDurationSeconds = 100 * 365 * 24 * 3600

func (a *API) whoami(w http.ResponseWriter, r *http.Request) {
	id := auth.FromRequest(r)
	resp := WhoAmI{
		Kind:   string(id.Kind),
		Scopes: scopeStrings(auth.AllScopes),
		Server: ServerInfo{AppURL: a.Auth.Hosts.AppOrigin(), ContentURL: a.Auth.Hosts.Content.String()},
	}
	if id.Kind == auth.KindToken {
		resp.Scopes = scopeStrings(id.Scopes)
		resp.Token = &TokenInfo{ID: id.TokenID, Name: id.TokenName, Scopes: resp.Scopes}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) setPassword(w http.ResponseWriter, r *http.Request) {
	var req SetPasswordRequest
	if !a.decode(w, r, &req) {
		return
	}
	if err := a.Auth.SetAdminPassword(r.Context(), req.Password); err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) createToken(w http.ResponseWriter, r *http.Request) {
	var req CreateTokenRequest
	if !a.decode(w, r, &req) {
		return
	}
	scopes := []auth.Scope{auth.ScopeRead, auth.ScopeWrite} // 默认不带 share，要显式给
	if len(req.Scopes) > 0 {
		var err error
		if scopes, err = auth.ParseScopes(strings.Join(req.Scopes, ",")); err != nil {
			a.fail(w, r, badRequest(err.Error()))
			return
		}
	}
	ttl, ok := a.ttl(w, r, req.ExpiresIn)
	if !ok {
		return
	}
	t, plain, err := a.Auth.CreateToken(r.Context(), req.Name, scopes, ttl)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	info := tokenInfo(t, time.Now())
	info.Token = plain
	writeJSON(w, http.StatusCreated, info)
}

func (a *API) listTokens(w http.ResponseWriter, r *http.Request) {
	ts, err := a.Auth.ListTokens(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	now := time.Now()
	resp := TokenList{Tokens: make([]TokenInfo, len(ts))}
	for i, t := range ts {
		resp.Tokens[i] = tokenInfo(t, now)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	found, err := a.Auth.RevokeToken(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !found {
		a.fail(w, r, notFound("no active API token with id "+strconv.FormatInt(id, 10)))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) createShare(w http.ResponseWriter, r *http.Request) {
	var req CreateShareRequest
	if !a.decode(w, r, &req) {
		return
	}
	ttl, ok := a.ttl(w, r, req.ExpiresIn)
	if !ok {
		return
	}
	name := r.PathValue("name")
	sh, err := a.Auth.CreateShare(r.Context(), name, auth.ShareOptions{Note: req.Note, Password: req.Password, TTL: ttl})
	if errors.Is(err, sql.ErrNoRows) {
		a.fail(w, r, notFound("no prototype named "+strconv.Quote(name)))
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, shareInfo(sh, time.Now()))
}

func (a *API) listShares(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("prototype")
	shares, err := a.Auth.ListShares(r.Context(), name)
	if errors.Is(err, sql.ErrNoRows) {
		a.fail(w, r, notFound("no prototype named "+strconv.Quote(name)))
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	now := time.Now()
	resp := ShareList{Shares: make([]ShareInfo, len(shares))}
	for i, sh := range shares {
		resp.Shares[i] = shareInfo(sh, now)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) revokeShare(w http.ResponseWriter, r *http.Request) {
	id, ok := a.pathID(w, r)
	if !ok {
		return
	}
	found, err := a.Auth.RevokeShare(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if !found {
		a.fail(w, r, notFound("no active share link with id "+strconv.FormatInt(id, 10)))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		a.fail(w, r, badRequest("invalid JSON body: "+err.Error()))
		return false
	}
	return true
}

func (a *API) ttl(w http.ResponseWriter, r *http.Request, secs int64) (time.Duration, bool) {
	if secs < 0 || secs > maxDurationSeconds {
		a.fail(w, r, badRequest("expires_in_seconds must be between 0 and 100 years"))
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
}

func (a *API) pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		a.fail(w, r, badRequest("id must be a positive integer"))
		return 0, false
	}
	return id, true
}

func badRequest(msg string) *publish.Error {
	return &publish.Error{Status: http.StatusBadRequest, Code: publish.CodeInvalidRequest, Message: msg}
}

func notFound(msg string) *publish.Error {
	return &publish.Error{Status: http.StatusNotFound, Code: publish.CodeNotFound, Message: msg}
}

func tokenInfo(t auth.Token, now time.Time) TokenInfo {
	status := "active"
	switch {
	case !t.RevokedAt.IsZero():
		status = "revoked"
	case !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt):
		status = "expired"
	}
	return TokenInfo{
		ID: t.ID, Name: t.Name, Hint: t.Hint, Scopes: scopeStrings(t.Scopes), Status: status,
		CreatedAt: rfc3339(t.CreatedAt), ExpiresAt: optTime(t.ExpiresAt), LastUsedAt: optTime(t.LastUsedAt), RevokedAt: optTime(t.RevokedAt),
	}
}

func shareInfo(sh auth.Share, now time.Time) ShareInfo {
	return ShareInfo{
		ID: sh.ID, Prototype: sh.Prototype, URL: sh.URL, Note: sh.Note, HasPassword: sh.HasPassword,
		Status: sh.Status(now), CreatedAt: rfc3339(sh.CreatedAt), ExpiresAt: optTime(sh.ExpiresAt), RevokedAt: optTime(sh.RevokedAt),
	}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func optTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := rfc3339(t)
	return &s
}

func scopeStrings(sc []auth.Scope) []string {
	out := make([]string, len(sc))
	for i, s := range sc {
		out[i] = string(s)
	}
	return out
}
