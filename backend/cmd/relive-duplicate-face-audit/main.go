// Package main 实现 relive-duplicate-face-audit 离线只读 P0 审计 CLI。
//
// 工具以 SQLite 只读方式（mode=ro + PRAGMA query_only=ON）打开显式指定的数据库
// 副本，找出“同一物理照片文件（file_hash 相同）中相同 embedding 被归属到不同人物”
// 的 P0 冲突，输出可复核的照片 ID、人脸 ID 和人物 ID。
//
// 本工具不创建、合并、拆分或移动人物，不执行迁移，不修改任何业务数据，不写入
// cannot-link，不重建画像。最终处置由人工完成。
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 是可测试的入口：参数解析、只读打开、报告生成与输出。返回退出码。
func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		// parseArgs 已自行打印错误与 usage。
		return 2
	}

	report, err := buildReport(opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	out, err := render(report, opts)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if _, err := fmt.Fprint(stdout, out); err != nil {
		fmt.Fprintf(stderr, "error: write output: %v\n", err)
		return 1
	}
	if len(out) > 0 && out[len(out)-1] != '\n' {
		fmt.Fprintln(stdout)
	}
	return 0
}
