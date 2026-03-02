package util

import (
	"os"
	"syscall"
	"time"
)

// FileTimes 文件时间信息
type FileTimes struct {
	ModTime  time.Time  // 修改时间
	CreateTime *time.Time // 创建时间（可能为空）
}

// GetFileTimes 获取文件的修改时间和创建时间
// 注意：创建时间在 Unix 系统上可能不可用
func GetFileTimes(info os.FileInfo) FileTimes {
	modTime := info.ModTime()
	var createTime *time.Time

	// 尝试获取创建时间（平台相关）
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		// 在 Darwin (macOS) 上使用 ctimespec
		// 在 Linux 上使用 Ctim
		ctime := getCreateTime(stat)
		if ctime != nil {
			createTime = ctime
		}
	}

	return FileTimes{
		ModTime:    modTime,
		CreateTime: createTime,
	}
}
