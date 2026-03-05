// Package version 提供统一的版本信息管理
// 版本号从项目根目录的 VERSION 文件读取
package version

import (
	_ "embed"
	"fmt"
	"strings"
	"time"
)

//go:embed VERSION
var versionFile string

var (
	// Version 是当前版本号（从 VERSION 文件读取）
	Version = strings.TrimSpace(versionFile)

	// BuildTime 是编译时间（通过 -ldflags 注入）
	BuildTime = "unknown"

	// GitCommit 是 Git 提交哈希（通过 -ldflags 注入）
	GitCommit = "unknown"

	// GoVersion 是 Go 版本（运行时获取）
	GoVersion string
)

func init() {
	if Version == "" {
		Version = "dev"
	}
}

// Info 返回完整的版本信息字符串
func Info() string {
	if BuildTime == "unknown" {
		return fmt.Sprintf("%s", Version)
	}
	return fmt.Sprintf("%s (built: %s)", Version, BuildTime)
}

// FullInfo 返回包含所有细节的版本信息
func FullInfo() string {
	parts := []string{Version}
	if GitCommit != "unknown" {
		parts = append(parts, fmt.Sprintf("commit: %s", GitCommit))
	}
	if BuildTime != "unknown" {
		parts = append(parts, fmt.Sprintf("built: %s", BuildTime))
	}
	return strings.Join(parts, ", ")
}

// BuildTimeFormatted 返回格式化的构建时间
func BuildTimeFormatted() string {
	if BuildTime == "unknown" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, BuildTime)
	if err != nil {
		return BuildTime
	}
	return t.Format("2006-01-02 15:04:05")
}
