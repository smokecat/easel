package client

import (
	"context"
	"net/url"

	"github.com/smokecat/easel/internal/api"
)

// ListPrototypes 列出原型。
func (c *Client) ListPrototypes(ctx context.Context) ([]api.PrototypeInfo, error) {
	var resp api.PrototypeList
	if err := c.doJSON(ctx, "GET", "/api/v1/prototypes", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Prototypes, nil
}

// GetPrototype 返回一个原型。
func (c *Client) GetPrototype(ctx context.Context, name string) (*api.PrototypeInfo, error) {
	var resp api.PrototypeInfo
	if err := c.doJSON(ctx, "GET", "/api/v1/prototypes/"+url.PathEscape(name), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeletePrototype 删除原型。
func (c *Client) DeletePrototype(ctx context.Context, name string) error {
	return c.doJSON(ctx, "DELETE", "/api/v1/prototypes/"+url.PathEscape(name), nil, nil)
}
