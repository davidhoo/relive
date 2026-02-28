package util

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// HashFile 计算文件的 SHA256 哈希值
func HashFile(filePath string) (string, error) {
	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// 创建哈希器
	hasher := sha256.New()

	// 复制文件内容到哈希器
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	// 计算哈希值
	hashBytes := hasher.Sum(nil)
	hashString := hex.EncodeToString(hashBytes)

	return hashString, nil
}

// HashBytes 计算字节数组的 SHA256 哈希值
func HashBytes(data []byte) string {
	hasher := sha256.New()
	hasher.Write(data)
	hashBytes := hasher.Sum(nil)
	return hex.EncodeToString(hashBytes)
}
