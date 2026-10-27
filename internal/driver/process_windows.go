//go:build windows

package driver

import (
	"errors"
	"os"
	"syscall"
)

// processAlive 用进程句柄判断进程是否仍在运行。
// 只看 OpenProcess 是否成功会把已经退出但内核对象尚未释放的进程当成存活,
// WaitForSingleObject 返回 WAIT_TIMEOUT 才代表进程真的还在运行
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer p.Release()

	var alive bool
	err = p.WithHandle(func(handle uintptr) {
		rc, _ := syscall.WaitForSingleObject(syscall.Handle(handle), 0)
		alive = rc == syscall.WAIT_TIMEOUT
	})
	if errors.Is(err, os.ErrProcessDone) {
		return false
	}
	if err != nil {
		return true
	}
	return alive
}
