//go:build !linux

package proxy

// Process 是监听某个端口的进程身份。
type Process struct {
	Exe string
	Cwd string
}

// ListenerProcess 在非 Linux 平台上不可用：返回 ok=false，不做进程身份检查。
func ListenerProcess(int) (Process, bool) { return Process{}, false }
