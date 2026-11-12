package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/smokecat/easel/internal/api"
)

func (c *Client) doJSON(ctx context.Context, method, path string, in, out any) error {
	var hdr http.Header
	var body *bytes.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
		hdr = http.Header{"Content-Type": {"application/json"}}
	}
	if body == nil {
		return c.do(ctx, method, path, hdr, nil, out)
	}
	return c.do(ctx, method, path, hdr, body, out)
}

// WhoAmI 返回当前凭据的身份。
func (c *Client) WhoAmI(ctx context.Context) (*api.WhoAmI, error) {
	var resp api.WhoAmI
	if err := c.doJSON(ctx, "GET", "/api/v1/whoami", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SetAdminPassword 设置管理员密码（只能经本机 unix socket）。
func (c *Client) SetAdminPassword(ctx context.Context, password string) error {
	return c.doJSON(ctx, "PUT", "/api/v1/admin/password", api.SetPasswordRequest{Password: password}, nil)
}

// CreateToken 创建 API token，响应里带明文。
func (c *Client) CreateToken(ctx context.Context, req api.CreateTokenRequest) (*api.TokenInfo, error) {
	var resp api.TokenInfo
	if err := c.doJSON(ctx, "POST", "/api/v1/tokens", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListTokens 列出 API token。
func (c *Client) ListTokens(ctx context.Context) ([]api.TokenInfo, error) {
	var resp api.TokenList
	if err := c.doJSON(ctx, "GET", "/api/v1/tokens", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Tokens, nil
}

// RevokeToken 撤销 API token。
func (c *Client) RevokeToken(ctx context.Context, id int64) error {
	return c.doJSON(ctx, "DELETE", "/api/v1/tokens/"+strconv.FormatInt(id, 10), nil, nil)
}

// CreateShare 给原型创建分享链接。
func (c *Client) CreateShare(ctx context.Context, prototype string, req api.CreateShareRequest) (*api.ShareInfo, error) {
	var resp api.ShareInfo
	if err := c.doJSON(ctx, "POST", "/api/v1/prototypes/"+url.PathEscape(prototype)+"/shares", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListShares 列出分享链接；prototype 为空时列出全部。
func (c *Client) ListShares(ctx context.Context, prototype string) ([]api.ShareInfo, error) {
	path := "/api/v1/shares"
	if prototype != "" {
		path += "?" + url.Values{"prototype": {prototype}}.Encode()
	}
	var resp api.ShareList
	if err := c.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Shares, nil
}

// RevokeShare 撤销分享链接。
func (c *Client) RevokeShare(ctx context.Context, id int64) error {
	return c.doJSON(ctx, "DELETE", "/api/v1/shares/"+strconv.FormatInt(id, 10), nil, nil)
}
