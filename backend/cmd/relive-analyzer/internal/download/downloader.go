package download

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

const (
	defaultTempDir    = "~/.relive-analyzer/temp"
	maxTempSize       = 10 * 1024 * 1024 * 1024 // 10GB
	defaultTimeout    = 60 * time.Second
	defaultRetryCount = 3
)

// HTTPClient HTTP 客户端接口
type HTTPClient interface {
	DownloadPhoto(ctx context.Context, downloadURL string) (io.ReadCloser, error)
}

// Downloader 照片下载器
type Downloader struct {
	client        HTTPClient
	tempDir       string
	timeout       time.Duration
	retryCount    int
	maxTempSize   int64
	keepTempFiles bool

	// 下载追踪
	downloading   map[string]bool // photoID -> downloading
	downloadMutex sync.RWMutex

	// 磁盘使用追踪
	currentSize   int64
	sizeMutex     sync.RWMutex
}

// Option 下载器配置选项
type Option func(*Downloader)

// WithTempDir 设置临时目录
func WithTempDir(dir string) Option {
	return func(d *Downloader) {
		d.tempDir = dir
	}
}

// WithTimeout 设置下载超时
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

// WithMaxTempSize 设置临时目录最大大小
func WithMaxTempSize(size int64) Option {
	return func(d *Downloader) {
		d.maxTempSize = size
	}
}

// WithKeepTempFiles 设置是否保留临时文件
func WithKeepTempFiles(keep bool) Option {
	return func(d *Downloader) {
		d.keepTempFiles = keep
	}
}

// NewDownloader 创建下载器
func NewDownloader(client HTTPClient, opts ...Option) (*Downloader, error) {
	d := &Downloader{
		client:      client,
		tempDir:     defaultTempDir,
		timeout:     defaultTimeout,
		retryCount:  defaultRetryCount,
		maxTempSize: maxTempSize,
		downloading: make(map[string]bool),
	}

	for _, opt := range opts {
		opt(d)
	}

	// 处理 ~ 展开
	if strings.HasPrefix(d.tempDir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		d.tempDir = filepath.Join(home, d.tempDir[2:])
	}

	// 确保临时目录存在
	if err := os.MkdirAll(d.tempDir, 0755); err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	// 清理旧的临时文件
	if err := d.Cleanup(); err != nil {
		logger.Warnf("Failed to cleanup temp files: %v", err)
	}

	return d, nil
}

// Download 下载照片
func (d *Downloader) Download(ctx context.Context, photoID uint, downloadURL string) (string, error) {
	photoIDStr := fmt.Sprintf("%d", photoID)

	// 检查是否已在下载中
	d.downloadMutex.Lock()
	if d.downloading[photoIDStr] {
		d.downloadMutex.Unlock()
		return "", fmt.Errorf("photo %d is already being downloaded", photoID)
	}
	d.downloading[photoIDStr] = true
	d.downloadMutex.Unlock()

	defer func() {
		d.downloadMutex.Lock()
		delete(d.downloading, photoIDStr)
		d.downloadMutex.Unlock()
	}()

	// 检查磁盘空间
	if err := d.checkDiskSpace(); err != nil {
		return "", err
	}

	// 生成临时文件路径
	tempFile := d.generateTempPath(photoID)

	// 下载文件（带重试）
	var lastErr error
	for attempt := 0; attempt <= d.retryCount; attempt++ {
		if attempt > 0 {
			logger.Infof("Retrying download for photo %d (attempt %d/%d)", photoID, attempt, d.retryCount)
			time.Sleep(time.Second * time.Duration(attempt))
		}

		err := d.downloadOnce(ctx, downloadURL, tempFile)
		if err == nil {
			// 更新磁盘使用统计
			if info, err := os.Stat(tempFile); err == nil {
				d.sizeMutex.Lock()
				d.currentSize += info.Size()
				d.sizeMutex.Unlock()
			}

			logger.Debugf("Downloaded photo %d to %s", photoID, tempFile)
			return tempFile, nil
		}

		lastErr = err
		logger.Warnf("Download attempt %d failed for photo %d: %v", attempt+1, photoID, err)
	}

	return "", fmt.Errorf("download failed after %d attempts: %w", d.retryCount+1, lastErr)
}

// downloadOnce 单次下载
func (d *Downloader) downloadOnce(ctx context.Context, downloadURL, tempFile string) error {
	// 创建下载上下文（带超时）
	downloadCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	// 调用客户端下载
	reader, err := d.client.DownloadPhoto(downloadCtx, downloadURL)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer reader.Close()

	// 创建临时文件
	file, err := os.Create(tempFile)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer file.Close()

	// 写入文件
	written, err := io.Copy(file, reader)
	if err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("write file: %w", err)
	}

	logger.Debugf("Downloaded %d bytes to %s", written, tempFile)
	return nil
}

// Delete 删除临时文件
func (d *Downloader) Delete(tempFile string) error {
	if d.keepTempFiles {
		logger.Debugf("Keeping temp file: %s", tempFile)
		return nil
	}

	info, err := os.Stat(tempFile)
	if err != nil {
		return fmt.Errorf("stat temp file: %w", err)
	}

	if err := os.Remove(tempFile); err != nil {
		return fmt.Errorf("remove temp file: %w", err)
	}

	// 更新磁盘使用统计
	d.sizeMutex.Lock()
	d.currentSize -= info.Size()
	if d.currentSize < 0 {
		d.currentSize = 0
	}
	d.sizeMutex.Unlock()

	logger.Debugf("Deleted temp file: %s", tempFile)
	return nil
}

// Cleanup 清理所有临时文件
func (d *Downloader) Cleanup() error {
	entries, err := os.ReadDir(d.tempDir)
	if err != nil {
		return fmt.Errorf("read temp dir: %w", err)
	}

	var cleaned int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(d.tempDir, entry.Name())
		if err := os.Remove(path); err != nil {
			logger.Warnf("Failed to remove temp file %s: %v", path, err)
		} else {
			cleaned++
		}
	}

	if cleaned > 0 {
		logger.Infof("Cleaned up %d temp files", cleaned)
	}

	// 重置磁盘使用统计
	d.sizeMutex.Lock()
	d.currentSize = 0
	d.sizeMutex.Unlock()

	return nil
}

// generateTempPath 生成临时文件路径
func (d *Downloader) generateTempPath(photoID uint) string {
	filename := fmt.Sprintf("photo_%d_%d.tmp", photoID, time.Now().Unix())
	return filepath.Join(d.tempDir, filename)
}

// checkDiskSpace 检查磁盘空间
func (d *Downloader) checkDiskSpace() error {
	d.sizeMutex.RLock()
	currentSize := d.currentSize
	d.sizeMutex.RUnlock()

	if currentSize >= d.maxTempSize {
		return fmt.Errorf("temp directory size limit reached: %d/%d bytes", currentSize, d.maxTempSize)
	}

	return nil
}

// GetTempDir 获取临时目录路径
func (d *Downloader) GetTempDir() string {
	return d.tempDir
}

// GetCurrentSize 获取当前临时文件总大小
func (d *Downloader) GetCurrentSize() int64 {
	d.sizeMutex.RLock()
	defer d.sizeMutex.RUnlock()
	return d.currentSize
}

// SimpleHTTPClient 简单的 HTTP 客户端实现
type SimpleHTTPClient struct {
	httpClient *http.Client
	apiKey     string
}

// NewSimpleHTTPClient 创建简单 HTTP 客户端
func NewSimpleHTTPClient(apiKey string) *SimpleHTTPClient {
	return &SimpleHTTPClient{
		httpClient: &http.Client{Timeout: defaultTimeout},
		apiKey:     apiKey,
	}
}

// DownloadPhoto 实现 HTTPClient 接口
func (c *SimpleHTTPClient) DownloadPhoto(ctx context.Context, downloadURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	return resp.Body, nil
}
