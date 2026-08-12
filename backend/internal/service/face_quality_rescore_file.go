package service

import "os"

// osReadFile 包装 os.ReadFile，便于在测试中通过替换 readFile 变量 mock 文件读取。
func osReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
