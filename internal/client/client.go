// Package client 是 CLI 调服务端用的 HTTP 客户端。目前只支持本机 unix socket（免登录）；远端 token 登录后续加。
package client

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/smokecat/easel/internal/publish"
)

// Client 调 easel 的 HTTP API。
type Client struct {
	http *http.Client
	base string
	// Socket 是连接的 unix socket 路径，用于报错。
	Socket string
}

// ConnError 表示连不上服务端（socket 不存在、拒绝连接、超时）。
type ConnError struct {
	Target string
	Err    error
}

func (e *ConnError) Error() string {
	return fmt.Sprintf("cannot reach easel at %s: %v", e.Target, e.Err)
}
func (e *ConnError) Unwrap() error { return e.Err }

// APIError 是服务端返回的错误。
type APIError struct {
	Status int
	Body   publish.Error
}

func (e *APIError) Error() string { return e.Body.Error() }

// NewSocket 返回经 unix socket 连接的客户端。
func NewSocket(path string) *Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", path)
		},
		ResponseHeaderTimeout: 5 * time.Minute, // 大文件写盘和提交可能要一会儿
	}
	return &Client{http: &http.Client{Transport: tr}, base: "http://easel", Socket: path}
}

func (c *Client) do(ctx context.Context, method, path string, hdr http.Header, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ne net.Error
		var oe *net.OpError
		if errors.As(err, &oe) || errors.As(err, &ne) || errors.Is(err, os.ErrNotExist) {
			return &ConnError{Target: c.Socket, Err: err}
		}
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var eb publish.ErrorBody
		if json.Unmarshal(data, &eb) == nil && eb.Error != nil {
			return &APIError{Status: resp.StatusCode, Body: *eb.Error}
		}
		return &APIError{Status: resp.StatusCode, Body: publish.Error{Code: "http_error", Message: fmt.Sprintf("server returned %s", resp.Status)}}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// CreateVersion 是推送的第 1 步。
func (c *Client) CreateVersion(ctx context.Context, name string, req publish.CreateRequest) (*publish.CreateResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var resp publish.CreateResponse
	hdr := http.Header{"Content-Type": {"application/json"}}
	if err := c.do(ctx, "POST", "/api/v1/prototypes/"+name+"/versions", hdr, bytes.NewReader(body), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PutBlobs 是第 2 步：把 files 打成 tar 流式上传，条目名是哈希。
func (c *Client) PutBlobs(ctx context.Context, uploadID string, files []publish.LocalFile) (*publish.BlobsResponse, error) {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(writeBlobTar(pw, files)) }()
	var resp publish.BlobsResponse
	hdr := http.Header{"Content-Type": {"application/x-tar"}}
	err := c.do(ctx, "PUT", "/api/v1/uploads/"+uploadID+"/blobs", hdr, pr, &resp)
	_ = pr.Close()
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func writeBlobTar(w io.Writer, files []publish.LocalFile) error {
	tw := tar.NewWriter(w)
	for _, f := range files {
		if err := addBlob(tw, f); err != nil {
			return err
		}
	}
	return tw.Close()
}

func addBlob(tw *tar.Writer, f publish.LocalFile) error {
	src, err := os.Open(f.Abs)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := tw.WriteHeader(&tar.Header{Name: f.SHA256, Mode: 0o644, Size: f.Size, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	n, err := io.Copy(tw, io.LimitReader(src, f.Size))
	if err != nil {
		return err
	}
	if n != f.Size {
		return fmt.Errorf("%s changed while pushing; run the push again", f.Path)
	}
	return nil
}

// Commit 是第 3 步。
func (c *Client) Commit(ctx context.Context, uploadID string) (*publish.Result, error) {
	var res publish.Result
	if err := c.do(ctx, "POST", "/api/v1/uploads/"+uploadID+"/commit", nil, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
