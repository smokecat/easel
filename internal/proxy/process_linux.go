package proxy

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Process 是监听某个端口的进程身份。同一个项目重启 dev server 时 PID 会变，exe 和 cwd 不变。
type Process struct {
	Exe string
	Cwd string
}

// ListenerProcess 找出在回环地址（或通配地址）上监听 port 的进程。只能看到当前用户自己的进程；
// 找不到时返回 ok=false，调用方不做身份检查。
func ListenerProcess(port int) (Process, bool) {
	inodes := map[string]bool{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		listenInodes(f, port, inodes)
	}
	if len(inodes) == 0 {
		return Process{}, false
	}
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return Process{}, false
	}
	for _, p := range procs {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		dir := filepath.Join("/proc", p.Name())
		fds, err := os.ReadDir(filepath.Join(dir, "fd"))
		if err != nil {
			continue // 别的用户的进程读不到
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			if inodes[strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]")] {
				exe, _ := os.Readlink(filepath.Join(dir, "exe"))
				cwd, _ := os.Readlink(filepath.Join(dir, "cwd"))
				if exe == "" && cwd == "" {
					return Process{}, false
				}
				return Process{Exe: exe, Cwd: cwd}, true
			}
		}
	}
	return Process{}, false
}

// listenInodes 从 /proc/net/tcp{,6} 里找状态为 LISTEN（0A）、本地端口为 port 的 socket inode。
func listenInodes(path string, port int, out map[string]bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	want := fmt.Sprintf(":%04X", port)
	sc := bufio.NewScanner(f)
	sc.Scan() // 表头
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 || fields[3] != "0A" || !strings.HasSuffix(fields[1], want) {
			continue
		}
		out[fields[9]] = true
	}
}
