// Package config 处理服务端配置、CLI 凭据和项目级 easel.toml。
package config

import (
	"fmt"
	"net/url"
	"strings"
)

// Hosts 是后台和内容的公开地址。后台和原型内容必须在不同主机上。
//
// 内容主机是 <label>.<Content 的主机>：Content 为 https://easel.example.com 时，
// 原型 foo-abc123 的地址是 https://foo-abc123.easel.example.com/。
type Hosts struct {
	App     *url.URL
	Content *url.URL
}

// ParseHosts 解析并校验两个公开地址（只取 scheme 和 host，路径必须为空）。
func ParseHosts(app, content string) (Hosts, error) {
	a, err := parseOrigin("app URL", app)
	if err != nil {
		return Hosts{}, err
	}
	c, err := parseOrigin("content URL", content)
	if err != nil {
		return Hosts{}, err
	}
	h := Hosts{App: a, Content: c}
	if _, ok := h.Label(a.Host); ok || strings.EqualFold(a.Host, c.Host) {
		return Hosts{}, fmt.Errorf("app URL %s must not be the content host or one of its subdomains", app)
	}
	return h, nil
}

func parseOrigin(what, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%s %q must look like https://host[:port]", what, raw)
	}
	return &url.URL{Scheme: u.Scheme, Host: strings.ToLower(u.Host)}, nil
}

// ContentOrigin 返回某个内容标签的源，如 https://foo-abc123.easel.example.com。
func (h Hosts) ContentOrigin(label string) string {
	return h.Content.Scheme + "://" + label + "." + h.Content.Host
}

// AppOrigin 返回后台的源。
func (h Hosts) AppOrigin() string { return h.App.Scheme + "://" + h.App.Host }

// Label 判断请求的 Host 是否是内容主机，是则返回一级标签（小写）。
// 多级子域（a.b.<content>）不算内容主机，返回 ok=false。
func (h Hosts) Label(host string) (label string, ok bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	suffix := "." + h.Content.Host
	label, found := strings.CutSuffix(host, suffix)
	if !found || label == "" || strings.Contains(label, ".") {
		return "", false
	}
	return label, true
}

// IsContentDomain 判断 host 是否落在内容域里（包括内容域本身和多级子域），这些请求都不能交给后台。
func (h Hosts) IsContentDomain(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == h.Content.Host || strings.HasSuffix(host, "."+h.Content.Host)
}
