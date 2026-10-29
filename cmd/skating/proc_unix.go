//go:build !windows

package main

import (
	"errors"
	"syscall"
	"time"
)

// processAlive 判断指定 PID 的进程是否仍存在。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// stopProcess 尽力终止指定 PID：先 SIGTERM，等待最多 3s，仍未退出则 SIGKILL。
// 进程不存在时返回 errProcessGone。
func stopProcess(pid int) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return errProcessGone
		}
		return err
	}
	for i := 0; i < 30; i++ {
		if !processAlive(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
