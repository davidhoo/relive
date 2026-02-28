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
	AI      *AIHandler
}

// NewHandlers 创建所有处理器
func NewHandlers(db *gorm.DB, services *service.Services) *Handlers {
	handlers := &Handlers{
		System:  NewSystemHandler(db, services),
		Photo:   NewPhotoHandler(services.Photo),
		Display: NewDisplayHandler(services.Display, services.ESP32),
		ESP32:   NewESP32Handler(services.ESP32),
	}

	// AI Handler 可能为 nil（如果 AI 服务未配置）
	if services.AI != nil {
		handlers.AI = NewAIHandler(services.AI)
	}

	return handlers
}
