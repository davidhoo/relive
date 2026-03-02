//go:build linux
// +build linux

package util

import (
	"syscall"
	"time"
)

// getCreateTime 获取文件创建时间（Linux）
// Linux 通常没有创建时间，返回 nil
func getCreateTime(stat *syscall.Stat_t) *time.Time {
	// Linux 的 ext4 文件系统支持创建时间，但需要较新的内核
	// 这里返回 nil，表示创建时间不可用
	return nil
}
