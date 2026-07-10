package handler

import (
	"net/http"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/gin-gonic/gin"
)

// BackgroundHandler 暴露后台任务治理的只读状态。
type BackgroundHandler struct {
	coordinator   *service.BackgroundTaskCoordinator
	loadSampler   *service.BackgroundLoadSampler
	rebuildStatus func() *model.ProtoCacheRebuildStatusResponse
}

// NewBackgroundHandler 构造后台状态处理器。coordinator 可能为 nil（未注入时返回空快照），
// 不应阻塞 API。rebuildStatus 可选，用于附加 protoCache rebuild 进度快照（nil 时省略）。
func NewBackgroundHandler(coordinator *service.BackgroundTaskCoordinator, loadSampler *service.BackgroundLoadSampler, rebuildStatus ...func() *model.ProtoCacheRebuildStatusResponse) *BackgroundHandler {
	h := &BackgroundHandler{
		coordinator: coordinator,
		loadSampler: loadSampler,
	}
	if len(rebuildStatus) > 0 {
		h.rebuildStatus = rebuildStatus[0]
	}
	return h
}

// GetStatus 返回后台任务治理的只读快照。
// @Summary 后台任务状态
// @Description 返回后台任务治理准入控制器的当前快照（foreground/cooldown/running/load/thresholds）
// @Tags background
// @Produce json
// @Success 200 {object} model.Response{data=model.BackgroundTaskStatusResponse}
// @Router /api/v1/background/status [get]
func (h *BackgroundHandler) GetStatus(c *gin.Context) {
	resp := buildBackgroundStatusResponse(h.coordinator, h.loadSampler)
	if h.rebuildStatus != nil {
		resp.ProtoCacheRebuild = h.rebuildStatus()
	}
	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "后台任务状态",
		Data:    resp,
	})
}

// buildBackgroundStatusResponse 构造响应 DTO。coordinator 为 nil 时返回全零快照（不 panic）。
func buildBackgroundStatusResponse(coord *service.BackgroundTaskCoordinator, sampler *service.BackgroundLoadSampler) model.BackgroundTaskStatusResponse {
	resp := model.BackgroundTaskStatusResponse{
		AutoTasksEnabled: true,
		CapturedAt:       time.Now().UTC().Format(time.RFC3339),
		Cooldowns:        map[string]string{},
		Load: model.BackgroundLoadSnapshotResponse{
			Load1:        -1,
			CPUUserPct:   -1,
			CPUSystemPct: -1,
			CPUIOWaitPct: -1,
			MemUsedPct:   -1,
		},
	}
	if sampler != nil {
		snap := sampler.Sample()
		resp.Load = model.BackgroundLoadSnapshotResponse{
			Load1:        snap.Load1,
			CPUUserPct:   snap.CPUUserPct,
			CPUSystemPct: snap.CPUSystemPct,
			CPUIOWaitPct: snap.CPUIOWaitPct,
			MemUsedPct:   snap.MemUsedPct,
		}
		resp.CapturedAt = snap.CapturedAt.UTC().Format(time.RFC3339)
	}
	if coord == nil {
		return resp
	}
	status := coord.Status()
	resp.ForegroundActive = status.ForegroundActive
	resp.ForegroundCount = status.ForegroundCount
	resp.AutoTasksEnabled = status.AutoTasksEnabled
	resp.Thresholds = model.BackgroundThresholdsResponse{
		CPUPauseThreshold:    status.CPUPauseThreshold,
		IOWaitPauseThreshold: status.IOWaitPauseThreshold,
		MemoryPauseThreshold: status.MemoryPauseThreshold,
		DBLockedCooldownMs:   status.DBLockedCooldownMs,
	}
	for _, r := range status.Running {
		resp.Running = append(resp.Running, model.BackgroundTaskRuntimeResponse{
			Class:     string(r.Class),
			DedupeKey: r.DedupeKey,
			Priority:  string(r.Priority),
			StartedAt: model.FormatBackgroundTimeRFC3339(r.StartedAt),
		})
	}
	for class, until := range status.Cooldowns {
		resp.Cooldowns[string(class)] = model.FormatBackgroundTimeRFC3339(until)
	}
	for _, p := range status.PendingDedupe {
		resp.PendingDedupe = append(resp.PendingDedupe, model.BackgroundTaskDedupeResponse{
			Class: string(p.Class), DedupeKey: p.DedupeKey,
		})
	}
	return resp
}
