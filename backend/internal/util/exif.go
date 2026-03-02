package util

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// EXIFData EXIF 数据结构
type EXIFData struct {
	TakenAt      *time.Time
	CameraModel  string
	Width        int
	Height       int
	Orientation  int
	GPSLatitude  *float64
	GPSLongitude *float64
}

// ExtractEXIF 提取 EXIF 信息
func ExtractEXIF(filePath string) (*EXIFData, error) {
	// 首先尝试用 goexif 库读取
	data, err := extractEXIFWithGoExif(filePath)
	if err == nil && (data.TakenAt != nil || data.CameraModel != "") {
		return data, nil
	}

	// 如果失败或数据为空，尝试用 sips（macOS）
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".heic" || ext == ".heif" {
		return extractEXIFWithSips(filePath)
	}

	// 返回原始数据（可能为空）
	return data, nil
}

// extractEXIFWithGoExif 使用 goexif 库提取 EXIF
func extractEXIFWithGoExif(filePath string) (*EXIFData, error) {
	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// 解码 EXIF
	x, err := exif.Decode(file)
	if err != nil {
		// 没有 EXIF 数据不算错误，返回空数据
		return &EXIFData{}, nil
	}

	data := &EXIFData{}

	// 提取拍摄时间
	if tm, err := x.DateTime(); err == nil {
		data.TakenAt = &tm
	}

	// 提取相机型号
	if model, err := x.Get(exif.Model); err == nil {
		if modelStr, err := model.StringVal(); err == nil {
			data.CameraModel = modelStr
		}
	}

	// 提取图片尺寸
	if width, err := x.Get(exif.PixelXDimension); err == nil {
		if w, err := width.Int(0); err == nil {
			data.Width = w
		}
	}
	if height, err := x.Get(exif.PixelYDimension); err == nil {
		if h, err := height.Int(0); err == nil {
			data.Height = h
		}
	}

	// 提取方向
	if orientation, err := x.Get(exif.Orientation); err == nil {
		if o, err := orientation.Int(0); err == nil {
			data.Orientation = o
		}
	}

	// 提取 GPS 信息
	lat, lon, err := x.LatLong()
	if err == nil {
		data.GPSLatitude = &lat
		data.GPSLongitude = &lon
	}

	return data, nil
}

// extractEXIFWithSips 使用 macOS sips 命令提取 EXIF（用于 HEIC）
func extractEXIFWithSips(filePath string) (*EXIFData, error) {
	cmd := exec.Command("sips", "-g", "all", filePath)
	output, err := cmd.Output()
	if err != nil {
		return &EXIFData{}, nil // 不算错误，返回空数据
	}

	data := &EXIFData{}
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// 提取拍摄时间: creation: 2025:11:02 10:49:32
		if strings.HasPrefix(line, "creation:") {
			dateStr := strings.TrimPrefix(line, "creation:")
			dateStr = strings.TrimSpace(dateStr)
			if tm, err := time.Parse("2006:01:02 15:04:05", dateStr); err == nil {
				data.TakenAt = &tm
			}
		}

		// 提取相机型号: model: iPhone 14 Pro
		if strings.HasPrefix(line, "model:") {
			model := strings.TrimPrefix(line, "model:")
			data.CameraModel = strings.TrimSpace(model)
		}

		// 提取尺寸: pixelWidth: 4032
		if strings.HasPrefix(line, "pixelWidth:") {
			widthStr := strings.TrimPrefix(line, "pixelWidth:")
			if w, err := strconv.Atoi(strings.TrimSpace(widthStr)); err == nil {
				data.Width = w
			}
		}

		// 提取高度: pixelHeight: 3024
		if strings.HasPrefix(line, "pixelHeight:") {
			heightStr := strings.TrimPrefix(line, "pixelHeight:")
			if h, err := strconv.Atoi(strings.TrimSpace(heightStr)); err == nil {
				data.Height = h
			}
		}

		// GPS 信息（如果有）
		if strings.Contains(line, "latitude") {
			re := regexp.MustCompile(`latitude:\s*([-\d.]+)`)
			if matches := re.FindStringSubmatch(line); len(matches) > 1 {
				if lat, err := strconv.ParseFloat(matches[1], 64); err == nil {
					data.GPSLatitude = &lat
				}
			}
		}
		if strings.Contains(line, "longitude") {
			re := regexp.MustCompile(`longitude:\s*([-\d.]+)`)
			if matches := re.FindStringSubmatch(line); len(matches) > 1 {
				if lon, err := strconv.ParseFloat(matches[1], 64); err == nil {
					data.GPSLongitude = &lon
				}
			}
		}
	}

	return data, nil
}
