//go:build darwin
// +build darwin

package util

import (
	"syscall"
	"time"
)

// getCreateTime 获取文件创建时间（Darwin/macOS）
func getCreateTime(stat *syscall.Stat_t) *time.Time {
	if stat == nil {
		return nil
	}
	// macOS 使用 Ctimespec（状态改变时间）
	t := time.Unix(stat.Ctimespec.Sec, stat.Ctimespec.Nsec)
	return &t
}
