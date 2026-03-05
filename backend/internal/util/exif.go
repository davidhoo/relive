package util

import (
	"encoding/json"
	"fmt"
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

	// 如果失败或数据为空，尝试用 exiftool（支持 HEIC）
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".heic" || ext == ".heif" {
		// 先尝试 exiftool（Docker/Linux 环境）
		data, err := extractEXIFWithExifTool(filePath)
		if err != nil {
			fmt.Printf("[EXIF] exiftool failed for %s: %v\n", filePath, err)
		} else if data.TakenAt != nil || data.GPSLatitude != nil || data.CameraModel != "" {
			fmt.Printf("[EXIF] exiftool success for %s: GPS=%v,%v\n", filePath, data.GPSLatitude, data.GPSLongitude)
			return data, nil
		} else {
			fmt.Printf("[EXIF] exiftool returned empty data for %s\n", filePath)
		}
		// exiftool 失败或无数据，尝试 sips（macOS）
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

// extractEXIFWithExifTool 使用 exiftool 提取 EXIF（支持 HEIC，适用于 Linux/Docker）
func extractEXIFWithExifTool(filePath string) (*EXIFData, error) {
	// 检查 exiftool 是否可用
	if _, err := exec.LookPath("exiftool"); err != nil {
		return nil, fmt.Errorf("exiftool not found in PATH")
	}

	cmd := exec.Command("exiftool", "-DateTimeOriginal", "-GPSLatitude", "-GPSLongitude",
		"-GPSLatitudeRef", "-GPSLongitudeRef", "-Model", "-ImageWidth", "-ImageHeight",
		"-Orientation", "-j", "-charset", "UTF8", filePath)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("exiftool failed: %w, stderr: %s", err, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("exiftool failed: %w", err)
	}

	// exiftool -j 输出 JSON 数组
	var results []map[string]interface{}
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("parse exiftool output: %w", err)
	}

	if len(results) == 0 {
		return &EXIFData{}, nil
	}

	result := results[0]
	data := &EXIFData{}

	// 提取拍摄时间
	if dateStr, ok := result["DateTimeOriginal"].(string); ok && dateStr != "" {
		// 格式: "2023:10:15 14:30:00"
		if tm, err := time.Parse("2006:01:02 15:04:05", dateStr); err == nil {
			data.TakenAt = &tm
		}
	}

	// 提取相机型号
	if model, ok := result["Model"].(string); ok {
		data.CameraModel = model
	}

	// 提取尺寸
	if width, ok := result["ImageWidth"].(float64); ok {
		data.Width = int(width)
	}
	if height, ok := result["ImageHeight"].(float64); ok {
		data.Height = int(height)
	}

	// 提取方向
	if orientation, ok := result["Orientation"].(string); ok {
		if o, err := strconv.Atoi(orientation); err == nil {
			data.Orientation = o
		}
	}

	// 提取 GPS - exiftool 返回格式: "39.9042 N"
	if latStr, ok := result["GPSLatitude"].(string); ok && latStr != "" {
		lat := parseGPSCoordinate(latStr)
		if lat != nil {
			// 检查参考方向
			if latRef, ok := result["GPSLatitudeRef"].(string); ok && latRef == "S" {
				latValue := -*lat
				lat = &latValue
			}
			data.GPSLatitude = lat
		}
	}

	if lonStr, ok := result["GPSLongitude"].(string); ok && lonStr != "" {
		lon := parseGPSCoordinate(lonStr)
		if lon != nil {
			// 检查参考方向
			if lonRef, ok := result["GPSLongitudeRef"].(string); ok && lonRef == "W" {
				lonValue := -*lon
				lon = &lonValue
			}
			data.GPSLongitude = lon
		}
	}

	return data, nil
}

// parseGPSCoordinate 解析 GPS 坐标字符串，如 "39.9042 N" 或 "116.4074 E"
func parseGPSCoordinate(coord string) *float64 {
	coord = strings.TrimSpace(coord)
	// 移除方向标记
	coord = strings.TrimSuffix(coord, " N")
	coord = strings.TrimSuffix(coord, " S")
	coord = strings.TrimSuffix(coord, " E")
	coord = strings.TrimSuffix(coord, " W")
	coord = strings.TrimSpace(coord)

	if value, err := strconv.ParseFloat(coord, 64); err == nil {
		return &value
	}
	return nil
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

	// 如果 sips 没有提取到 GPS，尝试使用 mdls（HEIC 文件的 GPS 信息在 mdls 中更完整）
	if data.GPSLatitude == nil || data.GPSLongitude == nil {
		extractGPSWithMdls(filePath, data)
	}

	return data, nil
}

// extractGPSWithMdls 使用 macOS mdls 命令提取 GPS 信息（备选方案）
func extractGPSWithMdls(filePath string, data *EXIFData) {
	cmd := exec.Command("mdls", "-name", "kMDItemLatitude", "-name", "kMDItemLongitude", filePath)
	output, err := cmd.Output()
	if err != nil {
		return
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		// kMDItemLatitude = 40.04032216666667
		if strings.HasPrefix(line, "kMDItemLatitude") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				latStr := strings.TrimSpace(parts[1])
				if lat, err := strconv.ParseFloat(latStr, 64); err == nil {
					data.GPSLatitude = &lat
				}
			}
		}

		// kMDItemLongitude = 116.2705916666667
		if strings.HasPrefix(line, "kMDItemLongitude") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				lonStr := strings.TrimSpace(parts[1])
				if lon, err := strconv.ParseFloat(lonStr, 64); err == nil {
					data.GPSLongitude = &lon
				}
			}
		}
	}
}
