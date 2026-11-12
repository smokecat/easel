package auth

import (
	"sync"
	"time"
)

// limiter 按 key 计数失败次数：窗口内失败达到 free 次后开始指数退避锁定。
// 只在进程内计数（单进程部署）；重启后清零，可以接受。
type limiter struct {
	window time.Duration // 距上次失败超过这个时长就重新计数
	free   int           // 失败满这么多次后开始锁定
	base   time.Duration // 第一次锁定的时长，之后每次翻倍
	max    time.Duration

	mu      sync.Mutex
	entries map[string]*limitEntry
}

type limitEntry struct {
	fails       int
	lastFail    time.Time
	lockedUntil time.Time
}

func newLimiter(window time.Duration, free int, base, max time.Duration) *limiter {
	return &limiter{window: window, free: free, base: base, max: max, entries: map[string]*limitEntry{}}
}

// begin 开始一次尝试：锁定中时返回还要等多久（不计数）；否则先把这次当作失败记下再返回 0，
// 验证成功后调 reset。先计数再验证，并发请求就不能都赶在第一次失败入账前溜过去。
func (l *limiter) begin(key string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.entries[key]; ok && now.Before(e.lockedUntil) {
		return e.lockedUntil.Sub(now)
	}
	l.failLocked(key, now)
	return 0
}

func (l *limiter) failLocked(key string, now time.Time) {
	e, ok := l.entries[key]
	if !ok || now.Sub(e.lastFail) > l.window {
		if len(l.entries) > 4096 {
			l.pruneLocked(now)
		}
		e = &limitEntry{}
		l.entries[key] = e
	}
	e.fails++
	e.lastFail = now
	if over := e.fails - l.free + 1; over > 0 { // 第 free 次失败后开始锁定
		d := l.base
		for i := 1; i < over && d < l.max; i++ {
			d *= 2
		}
		e.lockedUntil = now.Add(min(d, l.max))
	}
}

// reset 在成功后清除计数。
func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func (l *limiter) pruneLocked(now time.Time) {
	for k, e := range l.entries {
		if now.Sub(e.lastFail) > l.window && !now.Before(e.lockedUntil) {
			delete(l.entries, k)
		}
	}
}
