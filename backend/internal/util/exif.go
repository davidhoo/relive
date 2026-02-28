package util

import (
	"os"
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
