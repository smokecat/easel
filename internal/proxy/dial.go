package proxy

import (
	"context"
	"errors"
	"net"
	"strconv"
	"time"
)

// dialTimeout 是单个地址的拨号超时；本机回环，连不上通常立刻返回 ECONNREFUSED。
const dialTimeout = 2 * time.Second

// loopbackAddrs 是一个端口的候选拨号地址：按 localhost 的语义先 IPv6 后 IPv4，用字面 IP、不经 DNS。
// Vite 等 dev server 在新版 Node 上默认只监听 ::1，只拨 127.0.0.1 会 502。
func loopbackAddrs(port int) []string {
	p := strconv.Itoa(port)
	return []string{net.JoinHostPort("::1", p), net.JoinHostPort("127.0.0.1", p)}
}

// dialLoopback 依次尝试 [::1]:port 和 127.0.0.1:port，返回第一个连上的连接。
func dialLoopback(ctx context.Context, port int) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	var errs []error
	for _, addr := range loopbackAddrs(port) {
		c, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

// Reachable 报告本机回环上是否有进程在监听 port。
func Reachable(ctx context.Context, port int) bool {
	c, err := dialLoopback(ctx, port)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
