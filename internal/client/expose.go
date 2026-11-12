package client

import (
	"context"
	"net/url"

	"github.com/smokecat/easel/internal/publish"
)

// Expose 把本机端口注册成原型的端口代理。
func (c *Client) Expose(ctx context.Context, name string, req publish.ExposeRequest) (*publish.ExposeResult, error) {
	var res publish.ExposeResult
	if err := c.doJSON(ctx, "PUT", "/api/v1/prototypes/"+url.PathEscape(name)+"/upstream", req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
