package handler

import (
	"net/http"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
)

// ExportHandler 导出处理器
type ExportHandler struct {
	service service.ExportService
}

// NewExportHandler 创建导出处理器
func NewExportHandler(service service.ExportService) *ExportHandler {
	return &ExportHandler{
		service: service,
	}
}

// Export 导出数据
// @Summary 导出数据
// @Description 导出照片数据和分析结果到指定路径
// @Tags Export
// @Accept json
// @Produce json
// @Param request body model.ExportRequest true "导出请求"
// @Success 200 {object} model.Response{data=model.ExportResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/export [post]
func (h *ExportHandler) Export(c *gin.Context) {
	var req model.ExportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: err.Error(),
			},
		})
		return
	}

	// 执行导出
	resp, err := h.service.Export(req.OutputPath, req.Analyzed != nil && *req.Analyzed)
	if err != nil {
		logger.Errorf("Export failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "EXPORT_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    resp,
		Message: "Export completed successfully",
	})
}

// Import 导入数据
// @Summary 导入数据
// @Description 从导出文件导入 AI 分析结果
// @Tags Export
// @Accept json
// @Produce json
// @Param request body model.ImportRequest true "导入请求"
// @Success 200 {object} model.Response{data=model.ImportResponse}
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/import [post]
func (h *ExportHandler) Import(c *gin.Context) {
	var req model.ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: err.Error(),
			},
		})
		return
	}

	// 执行导入
	resp, err := h.service.Import(req.InputPath)
	if err != nil {
		logger.Errorf("Import failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "IMPORT_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    resp,
		Message: "Import completed successfully",
	})
}

// CheckExport 检查导出数据
// @Summary 检查导出数据
// @Description 检查导出数据的完整性
// @Tags Export
// @Accept json
// @Produce json
// @Param request body map[string]string true "检查请求" example({"export_path": "/path/to/export"})
// @Success 200 {object} model.Response
// @Failure 400 {object} model.Response
// @Failure 500 {object} model.Response
// @Router /api/v1/export/check [post]
func (h *ExportHandler) CheckExport(c *gin.Context) {
	var req struct {
		ExportPath string `json:"export_path" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "INVALID_REQUEST",
				Message: err.Error(),
			},
		})
		return
	}

	// 检查导出数据
	if err := h.service.CheckExport(req.ExportPath); err != nil {
		logger.Errorf("Export check failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "CHECK_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Export data is valid",
	})
}
