package download

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// Downloader 文件下载器
type Downloader struct {
	client        *http.Client
	tempDir       string
	timeout       time.Duration
	retryCount    int
	keepTempFiles bool
}

// Option 下载器配置选项
type Option func(*Downloader)

// WithTempDir 设置临时目录
func WithTempDir(dir string) Option {
	return func(d *Downloader) {
		d.tempDir = dir
	}
}

// WithTimeout 设置超时
func WithTimeout(timeout time.Duration) Option {
	return func(d *Downloader) {
		d.timeout = timeout
	}
}

// WithRetryCount 设置重试次数
func WithRetryCount(count int) Option {
	return func(d *Downloader) {
		d.retryCount = count
	}
}

// WithKeepTempFiles 设置是否保留临时文件
func WithKeepTempFiles(keep bool) Option {
	return func(d *Downloader) {
		d.keepTempFiles = keep
	}
}

// NewDownloader 创建下载器
func NewDownloader(opts ...Option) (*Downloader, error) {
	d := &Downloader{
		client:        &http.Client{Timeout: 30 * time.Second},
		tempDir:       "~/.relive-people-worker/temp",
		timeout:       30 * time.Second,
		retryCount:    3,
		keepTempFiles: false,
	}

	for _, opt := range opts {
		opt(d)
	}

	// 展开 ~ 为 home 目录
	if d.tempDir[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err == nil {
			d.tempDir = filepath.Join(home, d.tempDir[2:])
		}
	}

	// 创建临时目录
	if err := os.MkdirAll(d.tempDir, 0755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	return d, nil
}

// Download 下载文件
func (d *Downloader) Download(url string, apiKey string) (string, error) {
	var lastErr error

	for attempt := 0; attempt <= d.retryCount; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		path, err := d.downloadOnce(url, apiKey)
		if err != nil {
			lastErr = err
			logger.Warnf("Download attempt %d failed: %v", attempt+1, err)
			continue
		}

		return path, nil
	}

	return "", fmt.Errorf("max retries exceeded: %w", lastErr)
}

// downloadOnce 单次下载
func (d *Downloader) downloadOnce(url string, apiKey string) (string, error) {
	// 创建临时文件
	tempFile, err := os.CreateTemp(d.tempDir, "download-*.jpg")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer tempFile.Close()

	// 创建请求
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("create request: %w", err)
	}

	// 设置请求头
	req.Header.Set("X-API-Key", apiKey)

	// 执行请求
	resp, err := d.client.Do(req)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 写入文件
	_, err = io.Copy(tempFile, resp.Body)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("write file: %w", err)
	}

	return tempFile.Name(), nil
}

// Cleanup 清理临时文件
func (d *Downloader) Cleanup(path string) {
	if d.keepTempFiles {
		return
	}
	if err := os.Remove(path); err != nil {
		logger.Warnf("Failed to remove temp file %s: %v", path, err)
	}
}

// CleanupAll 清理所有临时文件
func (d *Downloader) CleanupAll() {
	if d.keepTempFiles {
		return
	}
	if err := os.RemoveAll(d.tempDir); err != nil {
		logger.Warnf("Failed to remove temp dir %s: %v", d.tempDir, err)
	}
}
