package handler

import (
	"github.com/davidhoo/relive/internal/service"
	"gorm.io/gorm"
)

// Handlers 所有处理器的集合
type Handlers struct {
	System  *SystemHandler
	Photo   *PhotoHandler
	Display *DisplayHandler
	ESP32   *ESP32Handler
}

// NewHandlers 创建所有处理器
func NewHandlers(db *gorm.DB, services *service.Services) *Handlers {
	return &Handlers{
		System:  NewSystemHandler(db, services),
		Photo:   NewPhotoHandler(services.Photo),
		Display: NewDisplayHandler(services.Display, services.ESP32),
		ESP32:   NewESP32Handler(services.ESP32),
	}
}
