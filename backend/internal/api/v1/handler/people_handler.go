package handler

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PeopleHandler struct {
	service                service.PeopleService
	mergeSuggestionService service.PersonMergeSuggestionService
	personRepo             repository.PersonRepository
	faceRepo               repository.FaceRepository
	photoRepo              repository.PhotoRepository
	jobRepo                repository.PeopleJobRepository
	runtimeService         service.AnalysisRuntimeService
	identityProfileService service.PersonIdentityProfileService
	identityDecisionRepo   repository.PeopleIdentityDecisionRepository
	// faceQualityService 人脸质检审核服务（stats/reviews/quality-decision/restore-auto）。
	// nil 时相关接口返回 503（功能未启用）。
	faceQualityService service.FaceQualityService
	// faceQualityBackfill 存量质检审计后台任务（暂停/继续/进度接口）。nil 时返回 503。
	faceQualityBackfill *service.FaceQualityBackfill
	// faceQualityRescore 历史重评分运行管理服务（rescore-runs CRUD + pause/resume/cancel/restore-auto）。nil 时返回 503。
	faceQualityRescore service.FaceQualityRescoreService
	// backgroundCoordinator 让人物详情读请求（GetPerson/GetPersonPhotos/GetPersonFaces）
	// 注册 foreground scope：执行期间 P2 自动后台任务暂停启动新批次，已运行任务在批次
	// 边界让步，避免与详情页读请求争抢 NAS 磁盘。nil 时跳过注册（如测试）。
	backgroundCoordinator *service.BackgroundTaskCoordinator
	// personPhotoRepo 用于人物照片 cursor 分页在 person_photos 迁移完成后切换到派生表索引。
	// nil 时回退到原 JOIN 查询（迁移未完成或测试未注入）。
	personPhotoRepo repository.PersonPhotoRepository
	cfg             *config.Config
}

func NewPeopleHandler(service service.PeopleService, mergeSuggestionService service.PersonMergeSuggestionService, personRepo repository.PersonRepository, faceRepo repository.FaceRepository, photoRepo repository.PhotoRepository, jobRepo repository.PeopleJobRepository, identityProfileService service.PersonIdentityProfileService, identityDecisionRepo repository.PeopleIdentityDecisionRepository, cfg *config.Config) *PeopleHandler {
	return &PeopleHandler{
		service:                service,
		mergeSuggestionService: mergeSuggestionService,
		personRepo:             personRepo,
		faceRepo:               faceRepo,
		photoRepo:              photoRepo,
		jobRepo:                jobRepo,
		identityProfileService: identityProfileService,
		identityDecisionRepo:   identityDecisionRepo,
		cfg:                    cfg,
	}
}

// SetBackgroundCoordinator 注入后台任务准入控制器，供人物详情读请求注册 foreground scope。
func (h *PeopleHandler) SetBackgroundCoordinator(c *service.BackgroundTaskCoordinator) {
	h.backgroundCoordinator = c
}

// SetFaceQualityService 注入人脸质检审核服务。
func (h *PeopleHandler) SetFaceQualityService(s service.FaceQualityService) {
	h.faceQualityService = s
}

// SetFaceQualityBackfill 注入存量质检审计后台任务，供暂停/继续/进度接口使用。
func (h *PeopleHandler) SetFaceQualityBackfill(b *service.FaceQualityBackfill) {
	h.faceQualityBackfill = b
}

// SetFaceQualityRescore 注入历史重评分运行管理服务，供 rescore-runs 接口使用。
func (h *PeopleHandler) SetFaceQualityRescore(s service.FaceQualityRescoreService) {
	h.faceQualityRescore = s
}

// SetPersonPhotoRepo 注入人物照片派生表仓库，供 cursor 分页在迁移完成后切换到索引查询。
func (h *PeopleHandler) SetPersonPhotoRepo(r repository.PersonPhotoRepository) {
	h.personPhotoRepo = r
}

// beginForegroundRelease 注册一个 foreground scope 并返回 release 函数。
// coordinator 为 nil 时返回 no-op，保证测试与未注入场景不受影响。
func (h *PeopleHandler) beginForegroundRelease() func() {
	if h.backgroundCoordinator == nil {
		return func() {}
	}
	return h.backgroundCoordinator.BeginForeground()
}

// listPersonPhotosCursor 根据 person_photos 迁移是否完成选择查询路径：
//   - 迁移完成（ready）→ 走 person_photos 派生表索引，避免 DISTINCT/ORDER BY 临时 B-Tree；
//   - 迁移未完成 → 走原有 JOIN + DISTINCT + ORDER BY 查询，保证回填期间接口仍可用。
//
// ppRepo 为 nil 时也回退到旧查询（如测试未注入）。fallback 时记录原因日志，
// 便于排查“为何索引未启用”（迁移未完成 / 校验失败 / 仓库未注入）。
func (h *PeopleHandler) listPersonPhotosCursor(personID uint, cursor *repository.PersonPhotoCursor, pageSize int) ([]*model.Photo, bool, *repository.PersonPhotoCursor, error) {
	if h.personPhotoRepo != nil {
		ready, err := h.personPhotoRepo.MigrationReady(nil)
		if err != nil {
			logger.Warnf("person_photos MigrationReady check error, fallback to JOIN: %v", err)
		} else if ready {
			return h.photoRepo.ListPhotoSummariesByPersonIDCursorFromIndex(personID, cursor, pageSize, h.personPhotoRepo)
		} else {
			// 记录 fallback 原因：迁移未完成（backfilling/repairing/verifying）。
			status, _, _ := h.personPhotoRepo.GetMigrationStatus(nil)
			logger.Debugf("person_photos not ready (status=%q), fallback to JOIN+DISTINCT for person %d", status, personID)
		}
	}
	return h.photoRepo.ListPhotoSummariesByPersonIDCursor(personID, cursor, pageSize)
}

func (h *PeopleHandler) SetRuntimeService(runtimeService service.AnalysisRuntimeService) {
	h.runtimeService = runtimeService
}

func (h *PeopleHandler) ListPeople(c *gin.Context) {
	page, pageSize, ok := parsePagination(c)
	if !ok {
		return
	}

	hasAvatar := strings.TrimSpace(c.Query("has_avatar"))
	category := strings.TrimSpace(c.Query("category"))
	search := strings.ToLower(strings.TrimSpace(c.Query("search")))
	visibility := strings.TrimSpace(c.Query("visibility"))

	opts := repository.ListPeopleOptions{
		Page:       page,
		PageSize:   pageSize,
		Category:   category,
		Search:     search,
		HasAvatar:  hasAvatar == "true",
		Visibility: visibility,
	}

	people, total, err := h.personRepo.ListPeople(opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{
			Success: false,
			Error: &model.ErrorInfo{
				Code:    "LIST_FAILED",
				Message: err.Error(),
			},
		})
		return
	}

	items := make([]model.PersonResponse, 0, len(people))
	for _, person := range people {
		items = append(items, personToResponse(person, nil))
	}

	totalPages := 0
	if total > 0 {
		totalPages = (int(total) + pageSize - 1) / pageSize
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.PagedResponse{
			Items:      items,
			Total:      total,
			Page:       page,
			PageSize:   pageSize,
			TotalPages: totalPages,
		},
	})
}

func (h *PeopleHandler) GetPerson(c *gin.Context) {
	personID, ok := parseUintParam(c, "id", "Invalid person ID")
	if !ok {
		return
	}

	// 人物详情读请求注册 foreground scope：执行期间 P2 自动后台任务暂停启动新批次，
	// 避免与详情页读请求争抢 NAS 磁盘。defer 确保所有提前返回路径都释放。
	release := h.beginForegroundRelease()
	defer release()

	person, err := h.personRepo.GetByID(personID)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "GET_FAILED", err.Error())
		return
	}
	if person == nil {
		writePeopleError(c, http.StatusNotFound, "NOT_FOUND", "Person not found")
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    personToResponse(person, nil),
	})
}

func (h *PeopleHandler) GetPersonPhotos(c *gin.Context) {
	personID, ok := parseUintParam(c, "id", "Invalid person ID")
	if !ok {
		return
	}

	// 详情页照片读请求注册 foreground scope，让后台任务让路。defer 保证释放。
	release := h.beginForegroundRelease()
	defer release()

	// Cursor pagination mode: no COUNT, keyset pagination.
	if c.Query("pagination") == "cursor" {
		pageSize := 30
		if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 {
				writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid page size")
				return
			}
			if value > 200 {
				value = 200
			}
			pageSize = value
		}

		cp, err := decodeCursor(c.Query("cursor"), cursorKindPhotos)
		if err != nil {
			writeCursorError(c, err)
			return
		}

		var repoCursor *repository.PersonPhotoCursor
		if cp != nil {
			t := millisToTime(cp.TakenAt)
			repoCursor = &repository.PersonPhotoCursor{
				TakenAt: t,
				ID:      cp.ID,
			}
		}

		photos, hasMore, nextCursor, err := h.listPersonPhotosCursor(personID, repoCursor, pageSize)
		if err != nil {
			writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
			return
		}

		// Cursor 推进保护：hasMore=true 时 nextCursor 必须严格落在输入 cursor 之后。
		// 不满足则返回稳定错误 PAGINATION_STALLED，绝不让客户端拿到一个停滞/回退的 cursor
		// 反复请求同一页（线上曾出现 nextCursor 指回当前页 → 每秒数十次同 URL 请求风暴）。
		if hasMore {
			if err := assertCursorAdvanced(repoCursor, nextCursor); err != nil {
				writePeopleError(c, http.StatusInternalServerError, "PAGINATION_STALLED", err.Error())
				return
			}
		}

		nextCursorStr := ""
		if nextCursor != nil {
			nextCursorStr = encodeCursor(cursorPayload{
				Version: cursorVersion,
				Kind:    cursorKindPhotos,
				TakenAt: timeToMillis(nextCursor.TakenAt),
				ID:      nextCursor.ID,
			})
		}

		c.JSON(http.StatusOK, model.Response{
			Success: true,
			Data: model.CursorPagedResponse{
				Items:      photos,
				HasMore:    hasMore,
				NextCursor: nextCursorStr,
			},
		})
		return
	}

	// 支持分页（向后兼容：无参数返回全量）
	if c.Query("page") != "" || c.Query("page_size") != "" {
		page, pageSize, ok := parsePagination(c)
		if !ok {
			return
		}

		photos, total, err := h.photoRepo.ListPhotoSummariesByPersonIDPaginated(personID, page, pageSize)
		if err != nil {
			writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
			return
		}

		totalPages := 0
		if total > 0 {
			totalPages = (int(total) + pageSize - 1) / pageSize
		}

		c.JSON(http.StatusOK, model.Response{
			Success: true,
			Data: model.PagedResponse{
				Items:      photos,
				Total:      total,
				Page:       page,
				PageSize:   pageSize,
				TotalPages: totalPages,
			},
		})
		return
	}

	// 无分页参数：返回全量（精简列 + SQL 排序）
	photos, err := h.photoRepo.ListPhotoSummariesByPersonID(personID)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Data: photos})
}

func (h *PeopleHandler) GetPersonFaces(c *gin.Context) {
	personID, ok := parseUintParam(c, "id", "Invalid person ID")
	if !ok {
		return
	}

	// 详情页人脸读请求注册 foreground scope，让后台任务让路。defer 保证释放。
	release := h.beginForegroundRelease()
	defer release()

	// Cursor pagination mode: no COUNT, keyset pagination.
	if c.Query("pagination") == "cursor" {
		pageSize := 50
		if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 {
				writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid page size")
				return
			}
			if value > 200 {
				value = 200
			}
			pageSize = value
		}

		cp, err := decodeCursor(c.Query("cursor"), cursorKindFaces)
		if err != nil {
			writeCursorError(c, err)
			return
		}

		var repoCursor *repository.PersonFaceCursor
		if cp != nil {
			repoCursor = &repository.PersonFaceCursor{
				QualityScore: cp.QualityScore,
				ID:           cp.ID,
			}
		}

		faces, hasMore, nextCursor, err := h.faceRepo.ListByPersonIDCursor(personID, repoCursor, pageSize)
		if err != nil {
			writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
			return
		}

		resp := make([]model.FaceResponse, 0, len(faces))
		for _, face := range faces {
			resp = append(resp, faceToResponse(face))
		}

		nextCursorStr := ""
		if nextCursor != nil {
			nextCursorStr = encodeCursor(cursorPayload{
				Version:      cursorVersion,
				Kind:         cursorKindFaces,
				QualityScore: nextCursor.QualityScore,
				ID:           nextCursor.ID,
			})
		}

		c.JSON(http.StatusOK, model.Response{
			Success: true,
			Data: model.CursorPagedResponse{
				Items:      resp,
				HasMore:    hasMore,
				NextCursor: nextCursorStr,
		},
		})
		return
	}

	// 支持分页（向后兼容：无参数返回全量）
	if c.Query("page") != "" || c.Query("page_size") != "" {
		page, pageSize, ok := parsePagination(c)
		if !ok {
			return
		}

		faces, total, err := h.faceRepo.ListByPersonIDPaginated(personID, page, pageSize)
		if err != nil {
			writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
			return
		}

		resp := make([]model.FaceResponse, 0, len(faces))
		for _, face := range faces {
			resp = append(resp, faceToResponse(face))
		}

		totalPages := 0
		if total > 0 {
			totalPages = (int(total) + pageSize - 1) / pageSize
		}

		c.JSON(http.StatusOK, model.Response{
			Success: true,
			Data: model.PagedResponse{
				Items:      resp,
				Total:      total,
				Page:       page,
				PageSize:   pageSize,
				TotalPages: totalPages,
			},
		})
		return
	}

	// 无分页参数：返回全量（排除 embedding，SQL 排序）
	faces, err := h.faceRepo.ListByPersonIDSummary(personID)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	resp := make([]model.FaceResponse, 0, len(faces))
	for _, face := range faces {
		resp = append(resp, faceToResponse(face))
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Data: resp})
}

func (h *PeopleHandler) CalculateSimilarity(c *gin.Context) {
	personID1, ok := parseUintParam(c, "id", "Invalid person ID")
	if !ok {
		return
	}

	var req struct {
		TargetPersonID uint `json:"target_person_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if req.TargetPersonID == 0 {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "target_person_id is required")
		return
	}
	if req.TargetPersonID == personID1 {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "cannot compare with self")
		return
	}

	score, err := h.mergeSuggestionService.CalculateSimilarity(personID1, req.TargetPersonID)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: gin.H{
			"person_id_1":      personID1,
			"person_id_2":      req.TargetPersonID,
			"similarity_score": score,
			"merge_threshold":  h.mergeSuggestionService.MergeSuggestionThreshold(),
			"attach_threshold": h.mergeSuggestionService.AttachThreshold(),
		},
	})
}

func (h *PeopleHandler) UpdatePersonCategory(c *gin.Context) {
	personID, ok := parseUintParam(c, "id", "Invalid person ID")
	if !ok {
		return
	}

	var req model.UpdatePersonCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.service.UpdatePersonCategory(personID, req.Category); err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物类别已更新"})
}

func (h *PeopleHandler) UpdatePersonName(c *gin.Context) {
	personID, ok := parseUintParam(c, "id", "Invalid person ID")
	if !ok {
		return
	}

	var req model.UpdatePersonNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.service.UpdatePersonName(personID, req.Name); err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物姓名已更新"})
}

func (h *PeopleHandler) UpdatePersonAvatar(c *gin.Context) {
	personID, ok := parseUintParam(c, "id", "Invalid person ID")
	if !ok {
		return
	}

	var req model.UpdatePersonAvatarRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.service.UpdatePersonAvatar(personID, req.FaceID); err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物头像已更新"})
}

func (h *PeopleHandler) DissolvePerson(c *gin.Context) {
	personID, ok := parseUintParam(c, "id", "Invalid person ID")
	if !ok {
		return
	}

	faceCount, err := h.service.DissolvePerson(personID)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "人物已解散，人脸将由系统重新聚类",
		Data:    gin.H{"faces_released": faceCount},
	})
}

func (h *PeopleHandler) MergePeople(c *gin.Context) {
	var req model.MergePeopleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	// 创建异步合并任务
	jobID, err := h.service.MergePeopleAsync(req.TargetPersonID, req.SourcePersonIDs, model.PeopleMergeJobTypeMergeInto)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "合并任务已创建",
		Data: gin.H{
			"job_id": jobID,
			"status": "pending",
		},
	})
}

// UpdateVisibility 批量设置人物隐藏状态。单个操作复用同一接口。
// 仅修改 hidden 字段，不触发分类更新、聚类、合并建议重算或照片变更。
func (h *PeopleHandler) UpdateVisibility(c *gin.Context) {
	var req model.UpdatePeopleVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	// 去重 + 数量限制，防止超大批量请求
	seen := make(map[uint]struct{}, len(req.PersonIDs))
	ids := make([]uint, 0, len(req.PersonIDs))
	for _, id := range req.PersonIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "person_ids 不能为空")
		return
	}
	const maxVisibilityBatch = 500
	if len(ids) > maxVisibilityBatch {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "单次最多操作 500 个人物")
		return
	}

	updated, err := h.service.UpdateVisibility(ids, *req.Hidden)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "UPDATE_FAILED", err.Error())
		return
	}

	// 全部 ID 都不存在 → 明确错误；部分不存在则更新其余并返回实际更新数
	if updated == 0 {
		writePeopleError(c, http.StatusNotFound, "NOT_FOUND", "未找到任何指定的人物")
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "人物可见性已更新",
		Data: gin.H{
			"updated":       updated,
			"requested":     len(ids),
			"hidden":        *req.Hidden,
			"missing_count": len(ids) - int(updated),
		},
	})
}

// GetMergeJob 获取合并任务状态
func (h *PeopleHandler) GetMergeJob(c *gin.Context) {
	jobID, err := strconv.ParseUint(c.Param("job_id"), 10, 32)
	if err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_JOB_ID", "Invalid job ID")
		return
	}

	job, err := h.service.GetMergeJobStatus(uint(jobID))
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			writePeopleError(c, http.StatusNotFound, "NOT_FOUND", "Job not found")
			return
		}
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    job,
	})
}

func (h *PeopleHandler) SplitPerson(c *gin.Context) {
	var req model.SplitPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	person, rc, err := h.service.SplitPerson(req.SourcePersonID, req.FaceIDs)
	if err != nil {
		if errors.Is(err, service.ErrPeopleSplitConflict) {
			writePeopleError(c, http.StatusConflict, "SPLIT_ASSIGNMENT_CONFLICT", "所选人脸归属已发生变化，请刷新后重新选择")
			return
		}
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "人物已拆分",
		Data: gin.H{
			"person":               personToResponse(person, nil),
			"recluster_evaluated":  rc.Evaluated,
			"recluster_reassigned": rc.Reassigned,
			"recluster_iterations": rc.Iterations,
		},
	})
}

func (h *PeopleHandler) MoveFaces(c *gin.Context) {
	var req model.MoveFacesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	rc, err := h.service.MoveFaces(req.FaceIDs, req.TargetPersonID)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人脸已移动", Data: rc})
}

func (h *PeopleHandler) StartBackground(c *gin.Context) {
	task, err := h.service.StartBackground()
	if err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "START_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物后台任务已启动", Data: task})
}

func (h *PeopleHandler) StopBackground(c *gin.Context) {
	if err := h.service.StopBackground(); err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "STOP_FAILED", Message: err.Error()}})
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物后台任务停止请求已发送"})
}

func (h *PeopleHandler) GetTask(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{Success: true, Data: h.service.GetTaskStatus()})
}

func (h *PeopleHandler) GetStats(c *gin.Context) {
	stats, err := h.service.GetStats()
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "STATS_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: stats})
}

func (h *PeopleHandler) GetBackgroundLogs(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "Success",
		Data:    map[string]interface{}{"lines": h.service.GetBackgroundLogs()},
	})
}

func (h *PeopleHandler) GetMergeSuggestionTask(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{Success: true, Data: h.mergeSuggestionService.GetTask()})
}

func (h *PeopleHandler) GetMergeSuggestionStats(c *gin.Context) {
	stats, err := h.mergeSuggestionService.GetStats()
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "STATS_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: stats})
}

func (h *PeopleHandler) GetMergeSuggestionLogs(c *gin.Context) {
	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    map[string]interface{}{"lines": h.mergeSuggestionService.GetBackgroundLogs()},
	})
}

func (h *PeopleHandler) PauseMergeSuggestionTask(c *gin.Context) {
	if err := h.mergeSuggestionService.Pause(); err != nil {
		writeServiceFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物合并建议后台任务已暂停"})
}

func (h *PeopleHandler) ResumeMergeSuggestionTask(c *gin.Context) {
	if err := h.mergeSuggestionService.Resume(); err != nil {
		writeServiceFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物合并建议后台任务已恢复"})
}

func (h *PeopleHandler) RebuildMergeSuggestionTask(c *gin.Context) {
	if err := h.mergeSuggestionService.Rebuild(); err != nil {
		writeServiceFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物合并建议后台任务已重建"})
}

func (h *PeopleHandler) ListMergeSuggestions(c *gin.Context) {
	page, pageSize, ok := parsePagination(c)
	if !ok {
		return
	}

	items, total, err := h.mergeSuggestionService.ListPending(page, pageSize)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.PagedResponse{
			Items:      items,
			Total:      total,
			Page:       page,
			PageSize:   pageSize,
			TotalPages: totalPages,
		},
	})
}

func (h *PeopleHandler) GetMergeSuggestion(c *gin.Context) {
	suggestionID, ok := parseUintParam(c, "id", "Invalid suggestion ID")
	if !ok {
		return
	}

	item, err := h.mergeSuggestionService.GetPendingByID(suggestionID)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}
	if item == nil {
		writePeopleError(c, http.StatusNotFound, "NOT_FOUND", "Merge suggestion not found")
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Data: item})
}

func (h *PeopleHandler) ExcludeMergeSuggestionCandidates(c *gin.Context) {
	suggestionID, ok := parseUintParam(c, "id", "Invalid suggestion ID")
	if !ok {
		return
	}

	var req struct {
		CandidatePersonIDs []uint `json:"candidate_person_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.mergeSuggestionService.ExcludeCandidates(suggestionID, req.CandidatePersonIDs); err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "候选人物已剔除"})
}

func (h *PeopleHandler) ApplyMergeSuggestion(c *gin.Context) {
	suggestionID, ok := parseUintParam(c, "id", "Invalid suggestion ID")
	if !ok {
		return
	}

	var req struct {
		CandidatePersonIDs []uint `json:"candidate_person_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.mergeSuggestionService.ApplySuggestion(suggestionID, req.CandidatePersonIDs); err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物合并建议已应用"})
}

func (h *PeopleHandler) RescanByPath(c *gin.Context) {
	var req model.PeopleBatchEnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	count, err := h.service.EnqueueByPath(req.Path, model.PeopleJobSourceManual, 80)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "ENQUEUE_FAILED", err.Error())
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "人物重扫任务已加入队列",
		Data: gin.H{
			"count": count,
		},
	})
}

func (h *PeopleHandler) EnqueueUnprocessed(c *gin.Context) {
	count, err := h.service.EnqueueUnprocessed()
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "ENQUEUE_FAILED", err.Error())
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: fmt.Sprintf("已入队 %d 张未处理照片", count),
		Data: gin.H{
			"enqueued": count,
		},
	})
}

func (h *PeopleHandler) EnqueuePhotoForDetection(c *gin.Context) {
	photoID, ok := parseUintParam(c, "id", "Invalid photo ID")
	if !ok {
		return
	}

	// force=true: 如果已有识别结果，会重新检测
	force := c.Query("force") == "true"

	if err := h.service.EnqueuePhoto(photoID, model.PeopleJobSourceManual, 80, force); err != nil {
		if errors.Is(err, service.ErrPhotoAnalysisPending) {
			writePeopleError(c, http.StatusConflict, "PHOTO_ANALYSIS_PENDING", err.Error())
			return
		}
		if errors.Is(err, service.ErrPhotoPeopleExcluded) {
			writePeopleError(c, http.StatusConflict, "PHOTO_PEOPLE_EXCLUDED", err.Error())
			return
		}
		writePeopleError(c, http.StatusInternalServerError, "ENQUEUE_FAILED", err.Error())
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "人脸识别任务已入队",
		Data: gin.H{
			"photo_id": photoID,
			"force":    force,
		},
	})
}

func (h *PeopleHandler) ResetAllPeople(c *gin.Context) {
	go func() {
		count, err := h.service.ResetAllPeople()
		if err != nil {
			logger.Errorf("reset all people failed: %v", err)
			return
		}
		if _, err := h.service.StartBackground(); err != nil {
			logger.Warnf("reset: background task start failed: %v", err)
		}
		logger.Infof("reset all people complete: %d photos enqueued", count)
	}()

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "全量重建已在后台启动",
	})
}

func (h *PeopleHandler) GetPhotoPeople(c *gin.Context) {
	photoID, ok := parseUintParam(c, "id", "Invalid photo ID")
	if !ok {
		return
	}

	resp, err := h.buildPhotoPeopleResponse(photoID)
	if err != nil {
		status := http.StatusInternalServerError
		code := "LIST_FAILED"
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
			code = "NOT_FOUND"
		}
		writePeopleError(c, status, code, err.Error())
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Data: resp})
}

// AssignFacePerson 针对单张人脸执行“改名”归属变更，返回更新后的照片人物信息。
func (h *PeopleHandler) AssignFacePerson(c *gin.Context) {
	faceID, ok := parseUintParam(c, "id", "Invalid face ID")
	if !ok {
		return
	}

	var req model.FacePersonAssignmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	photoID, err := h.service.AssignFacePerson(faceID, req)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}

	resp, err := h.buildPhotoPeopleResponse(photoID)
	if err != nil {
		// 归属变更已成功，仅刷新返回失败时不回滚，向前端报错让其手动刷新。
		writePeopleError(c, http.StatusInternalServerError, "REFRESH_FAILED", err.Error())
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "人脸归属已更新",
		Data:    resp,
	})
}

// UpdateFaceExclusion 标记或恢复人脸排除状态
func (h *PeopleHandler) UpdateFaceExclusion(c *gin.Context) {
	var req model.UpdateFaceExclusionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if req.Excluded && !model.IsValidExclusionReason(req.Reason) {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REASON",
			"排除原因必须为 non_face 或 low_quality")
		return
	}

	result, err := h.service.UpdateFaceExclusion(req.FaceIDs, req.Excluded, req.Reason)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}

	message := "人脸已排除"
	if !req.Excluded {
		message = "人脸已恢复"
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: message,
		Data:    result,
	})
}

// ---- 人脸质检审核接口 ----

// GetFaceQualityStats 全局质检统计。
func (h *PeopleHandler) GetFaceQualityStats(c *gin.Context) {
	if h.faceQualityService == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检功能未启用")
		return
	}
	stats, err := h.faceQualityService.GetStats()
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "FACE_QUALITY_STATS_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: stats})
}

// ListFaceQualityReviews 审核页列表（支持 state/reason/source/rule_version/时间范围/分页）。
func (h *PeopleHandler) ListFaceQualityReviews(c *gin.Context) {
	if h.faceQualityService == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检功能未启用")
		return
	}
	page, pageSize, ok := parsePagination(c)
	if !ok {
		return
	}
	q := model.FaceQualityReviewQuery{
		State:       strings.TrimSpace(c.Query("state")),
		Reason:      strings.TrimSpace(c.Query("reason")),
		Source:      strings.TrimSpace(c.Query("source")),
		RuleVersion: strings.TrimSpace(c.Query("rule_version")),
		StartTime:   strings.TrimSpace(c.Query("start_time")),
		EndTime:     strings.TrimSpace(c.Query("end_time")),
		Page:        page,
		PageSize:    pageSize,
	}
	page2, err := h.faceQualityService.ListReviews(q)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "FACE_QUALITY_LIST_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: page2})
}

// ApplyFaceQualityDecision 人工质检决策（批量确认排除/改判/接受/恢复）。
func (h *PeopleHandler) ApplyFaceQualityDecision(c *gin.Context) {
	if h.faceQualityService == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检功能未启用")
		return
	}
	var req model.FaceQualityDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if !model.IsValidQualityReviewAction(req.Action) {
		writePeopleError(c, http.StatusBadRequest, "INVALID_ACTION", "无效的审核动作")
		return
	}
	result, err := h.faceQualityService.ApplyQualityDecision(req)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "质检决策已应用", Data: result})
}

// RestoreAutoFaceQuality 按规则版本恢复自动排除的样本（回滚/阈值修正用）。
func (h *PeopleHandler) RestoreAutoFaceQuality(c *gin.Context) {
	if h.faceQualityService == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检功能未启用")
		return
	}
	var req model.FaceQualityRestoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 500
	}
	result, err := h.faceQualityService.RestoreAuto(req.RuleVersion, limit)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "已按规则版本恢复自动排除样本", Data: result})
}

// GetFaceQualityBackfillStatus 存量质检审计后台任务状态（进度/暂停态）。
func (h *PeopleHandler) GetFaceQualityBackfillStatus(c *gin.Context) {
	if h.faceQualityBackfill == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检审计任务未启用")
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: gin.H{
		"paused":         h.faceQualityBackfill.IsPaused(),
		"last_face_id":   h.faceQualityBackfill.Progress(),
		"progress_key":   "migration.face_quality_backfill_v1",
	}})
}

// PauseFaceQualityBackfill 暂停存量质检审计。
func (h *PeopleHandler) PauseFaceQualityBackfill(c *gin.Context) {
	if h.faceQualityBackfill == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检审计任务未启用")
		return
	}
	h.faceQualityBackfill.Pause()
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "存量质检审计已暂停"})
}

// ResumeFaceQualityBackfill 恢复存量质检审计。
func (h *PeopleHandler) ResumeFaceQualityBackfill(c *gin.Context) {
	if h.faceQualityBackfill == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检审计任务未启用")
		return
	}
	h.faceQualityBackfill.Resume()
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "存量质检审计已恢复"})
}

// ---- 历史重评分运行接口 ----

// rescoreRunResponse 把 model 转为 DTO 响应视图。
func rescoreRunResponse(r *model.FaceQualityRescoreRun) model.FaceQualityRescoreRunResponse {
	return model.FaceQualityRescoreRunResponse{
		ID:                  r.ID,
		Mode:                r.Mode,
		ApplyMode:           r.ApplyMode,
		Status:              r.Status,
		TargetPhotoCount:    r.TargetPhotoCount,
		TargetFaceCount:     r.TargetFaceCount,
		ProcessedPhotoCount: r.ProcessedPhotoCount,
		ProcessedFaceCount:  r.ProcessedFaceCount,
		AcceptedCount:       r.AcceptedCount,
		ReviewRequiredCount: r.ReviewRequiredCount,
		AutoExcludedCount:   r.AutoExcludedCount,
		RetryableCount:      r.RetryableCount,
		LastError:           r.LastError,
		StartedAt:           r.StartedAt,
		CompletedAt:         r.CompletedAt,
		RuleVersion:         r.RuleVersion,
		ModelVersion:        r.ModelVersion,
		PhotoLimit:          r.PhotoLimit,
		CreatedAt:           r.CreatedAt,
		UpdatedAt:           r.UpdatedAt,
	}
}

// CreateFaceQualityRescoreRun 创建历史重评分运行。
// 校准强制 shadow；full/enforce 需已完成 calibration；同时只允许一个活跃 run。
func (h *PeopleHandler) CreateFaceQualityRescoreRun(c *gin.Context) {
	if h.faceQualityRescore == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检重评分功能未启用")
		return
	}
	var req model.FaceQualityRescoreRunCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	// full 模式必须 enforce（不接受 full+shadow，无意义且误导）。service 内部会归一化
	// calibration→shadow、full→enforce，故此处固定传 enforce 即可。
	run, err := h.faceQualityRescore.CreateRun(req.Mode, model.FaceQualityRescoreApplyModeEnforce, req.PhotoLimit)
	if err != nil {
		writeRescoreRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: rescoreRunResponse(run)})
}

// ListFaceQualityRescoreRuns 列出重评分运行（最近优先）。
func (h *PeopleHandler) ListFaceQualityRescoreRuns(c *gin.Context) {
	if h.faceQualityRescore == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检重评分功能未启用")
		return
	}
	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	runs, err := h.faceQualityRescore.ListRuns(limit)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "RESCORE_LIST_FAILED", err.Error())
		return
	}
	items := make([]model.FaceQualityRescoreRunResponse, 0, len(runs))
	for _, r := range runs {
		items = append(items, rescoreRunResponse(r))
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: gin.H{"items": items}})
}

// GetFaceQualityRescoreRun 获取单个运行详情。
func (h *PeopleHandler) GetFaceQualityRescoreRun(c *gin.Context) {
	if h.faceQualityRescore == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检重评分功能未启用")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid run id")
		return
	}
	run, err := h.faceQualityRescore.GetRun(uint(id))
	if err != nil {
		writeRescoreRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: rescoreRunResponse(run)})
}

// PauseFaceQualityRescoreRun 暂停运行。
func (h *PeopleHandler) PauseFaceQualityRescoreRun(c *gin.Context) {
	if h.faceQualityRescore == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检重评分功能未启用")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid run id")
		return
	}
	if err := h.faceQualityRescore.Pause(uint(id)); err != nil {
		writeRescoreRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "重评分运行已暂停"})
}

// ResumeFaceQualityRescoreRun 恢复运行（processing item 回到 pending）。
func (h *PeopleHandler) ResumeFaceQualityRescoreRun(c *gin.Context) {
	if h.faceQualityRescore == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检重评分功能未启用")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid run id")
		return
	}
	if err := h.faceQualityRescore.Resume(uint(id)); err != nil {
		writeRescoreRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "重评分运行已恢复"})
}

// CancelFaceQualityRescoreRun 取消运行（停止未处理 item，不删除审计记录）。
func (h *PeopleHandler) CancelFaceQualityRescoreRun(c *gin.Context) {
	if h.faceQualityRescore == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检重评分功能未启用")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid run id")
		return
	}
	if err := h.faceQualityRescore.Cancel(uint(id)); err != nil {
		writeRescoreRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "重评分运行已取消"})
}

// RestoreAutoFaceQualityRescoreRun 按运行恢复自动排除（只恢复 rescore_run_id 匹配的样本）。
func (h *PeopleHandler) RestoreAutoFaceQualityRescoreRun(c *gin.Context) {
	if h.faceQualityRescore == nil {
		writePeopleError(c, http.StatusServiceUnavailable, "FACE_QUALITY_UNAVAILABLE", "人脸质检重评分功能未启用")
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid run id")
		return
	}
	limit := 0
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	result, err := h.faceQualityRescore.RestoreAuto(uint(id), limit)
	if err != nil {
		writeRescoreRunError(c, err)
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Message: "已按运行恢复自动排除样本", Data: result})
}

// writeRescoreRunError 把重评分服务错误映射为统一响应与错误码。
func writeRescoreRunError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrRescoreRunConflict):
		writePeopleError(c, http.StatusConflict, "RESCORE_RUN_CONFLICT", err.Error())
	case errors.Is(err, service.ErrRescoreCalibrationRequired):
		writePeopleError(c, http.StatusConflict, "RESCORE_CALIBRATION_REQUIRED", err.Error())
	case errors.Is(err, service.ErrRescoreRunNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		writePeopleError(c, http.StatusNotFound, "RESCORE_NOT_FOUND", err.Error())
	default:
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			writePeopleError(c, http.StatusNotFound, "RESCORE_NOT_FOUND", err.Error())
			return
		}
		writePeopleError(c, http.StatusInternalServerError, "RESCORE_OPERATION_FAILED", err.Error())
	}
}

// buildPhotoPeopleResponse 复用 GetPhotoPeople 与 AssignFacePerson 的数据组装逻辑。
func (h *PeopleHandler) buildPhotoPeopleResponse(photoID uint) (model.PhotoPersonResponse, error) {
	photo, err := h.photoRepo.GetByID(photoID)
	if err != nil {
		return model.PhotoPersonResponse{}, err
	}

	faces, err := h.faceRepo.ListByPhotoID(photoID)
	if err != nil {
		return model.PhotoPersonResponse{}, err
	}

	personIDs := uniquePersonIDs(faces)
	people, err := h.personRepo.ListByIDs(personIDs)
	if err != nil {
		return model.PhotoPersonResponse{}, err
	}

	facesByPerson := make(map[uint][]model.FaceResponse, len(personIDs))
	var excludedFaces []model.FaceResponse
	var pendingReviewFaces []model.FaceResponse
	for _, face := range faces {
		switch face.ClusterStatus {
		case model.FaceClusterStatusExcluded:
			excludedFaces = append(excludedFaces, faceToResponse(face))
			continue
		case model.FaceClusterStatusReviewRequired:
			// 待质检样本单独输出，照片详情页据此提示“待质检”，
			// 不与普通待识别（pending 无 person_id）混淆。
			pendingReviewFaces = append(pendingReviewFaces, faceToResponse(face))
			continue
		}
		if face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		facesByPerson[*face.PersonID] = append(facesByPerson[*face.PersonID], faceToResponse(face))
	}

	respPeople := make([]model.PersonResponse, 0, len(people))
	for _, person := range people {
		personFaces := facesByPerson[person.ID]
		sort.Slice(personFaces, func(i, j int) bool {
			if personFaces[i].QualityScore != personFaces[j].QualityScore {
				return personFaces[i].QualityScore > personFaces[j].QualityScore
			}
			return personFaces[i].ID < personFaces[j].ID
		})
		respPeople = append(respPeople, personToResponse(person, personFaces))
	}

	return model.PhotoPersonResponse{
		PhotoID:            photo.ID,
		FaceProcessStatus:  photo.FaceProcessStatus,
		FaceCount:          photo.FaceCount,
		TopPersonCategory:  photo.TopPersonCategory,
		People:             respPeople,
		ExcludedFaces:      excludedFaces,
		PendingReviewFaces: pendingReviewFaces,
	}, nil
}

func (h *PeopleHandler) GetFaceThumbnail(c *gin.Context) {
	faceID, ok := parseUintParam(c, "id", "Invalid face ID")
	if !ok {
		return
	}

	face, err := h.faceRepo.GetByID(faceID)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "GET_FAILED", err.Error())
		return
	}
	if face == nil {
		writePeopleError(c, http.StatusNotFound, "NOT_FOUND", "Face not found")
		return
	}

	if strings.TrimSpace(face.ThumbnailPath) == "" {
		photo, photoErr := h.photoRepo.GetByID(face.PhotoID)
		if photoErr != nil {
			writePeopleError(c, http.StatusInternalServerError, "GET_FAILED", photoErr.Error())
			return
		}
		// Prefer display thumbnail — already EXIF-oriented and correctly rotated.
		sourcePath := photo.FilePath
		rotation := photo.ManualRotation
		if photo.ThumbnailPath != "" {
			thumbFullPath := filepath.Join(thumbnailRoot(h.cfg), photo.ThumbnailPath)
			if _, statErr := os.Stat(thumbFullPath); statErr == nil {
				sourcePath = thumbFullPath
				rotation = 0
			}
		}
		thumbnailPath, genErr := util.GenerateFaceThumbnail(sourcePath, thumbnailRoot(h.cfg), face.BBoxX, face.BBoxY, face.BBoxWidth, face.BBoxHeight, rotation)
		if genErr != nil {
			writePeopleError(c, http.StatusInternalServerError, "GENERATE_FAILED", genErr.Error())
			return
		}
		// 重新生成人脸缩略图后同步刷新 updated_at，使前端 ?v=updated_at 版本参数变化，
		// 旧的 immutable 缓存失效，避免长期展示旧人脸图。
		if updateErr := h.faceRepo.UpdateFields(face.ID, map[string]interface{}{
			"thumbnail_path": thumbnailPath,
			"updated_at":     time.Now(),
		}); updateErr != nil {
			writePeopleError(c, http.StatusInternalServerError, "UPDATE_FAILED", updateErr.Error())
			return
		}
		face.ThumbnailPath = thumbnailPath
	}

	fullPath, err := resolveThumbnailPath(h.cfg, face.ThumbnailPath)
	if err != nil {
		writePeopleError(c, http.StatusNotFound, "NOT_FOUND", "Face thumbnail not found")
		return
	}
	if _, err := os.Stat(fullPath); err != nil {
		writePeopleError(c, http.StatusNotFound, "NOT_FOUND", "Face thumbnail not found")
		return
	}

	if contentType := mime.TypeByExtension(filepath.Ext(fullPath)); contentType != "" {
		c.Header("Content-Type", contentType)
	}
	// 人脸缩略图基于 face id + bbox + 原图路径生成稳定文件名（util.GenerateDerivedImagePath），
	// 内容变化（重新生成、bbox 变更、旋转）时文件路径随之改变。前端通过版本化 URL（face.updated_at）
	// 区分版本，配合长期私有浏览器缓存，避免对 NAS 重复发起缩略图校验请求。private 防止共享缓存泄露受保护图片。
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	c.File(fullPath)
}

func (h *PeopleHandler) ensurePersonExists(c *gin.Context, personID uint) bool {
	person, err := h.personRepo.GetByID(personID)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "GET_FAILED", err.Error())
		return false
	}
	if person == nil {
		writePeopleError(c, http.StatusNotFound, "NOT_FOUND", "Person not found")
		return false
	}
	return true
}

func parsePagination(c *gin.Context) (int, int, bool) {
	page := 1
	pageSize := 20

	if raw := strings.TrimSpace(c.Query("page")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid page")
			return 0, 0, false
		}
		page = value
	}

	if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid page size")
			return 0, 0, false
		}
		if value > 200 {
			value = 200
		}
		pageSize = value
	}

	return page, pageSize, true
}

func parseUintParam(c *gin.Context, name string, message string) (uint, bool) {
	value, err := strconv.ParseUint(c.Param(name), 10, 32)
	if err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", message)
		return 0, false
	}
	return uint(value), true
}

func writePeopleError(c *gin.Context, status int, code string, message string) {
	c.JSON(status, model.Response{
		Success: false,
		Error: &model.ErrorInfo{
			Code:    code,
			Message: message,
		},
	})
}

func writeServiceFailure(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	code := "OPERATION_FAILED"
	message := err.Error()
	if errors.Is(err, service.ErrPersonHidden) {
		status = http.StatusConflict
		code = "PERSON_HIDDEN"
		message = err.Error()
	}
	if errors.Is(err, gorm.ErrRecordNotFound) || strings.Contains(strings.ToLower(message), "not found") {
		status = http.StatusNotFound
		code = "NOT_FOUND"
	}
	writePeopleError(c, status, code, message)
}

func personToResponse(person *model.Person, faces []model.FaceResponse) model.PersonResponse {
	if person == nil {
		return model.PersonResponse{}
	}
	return model.PersonResponse{
		ID:                   person.ID,
		Name:                 person.Name,
		Category:             person.Category,
		RepresentativeFaceID: person.RepresentativeFaceID,
		HasAvatar:            person.RepresentativeFaceID != nil,
		AvatarLocked:         person.AvatarLocked,
		FaceCount:            person.FaceCount,
		PhotoCount:           person.PhotoCount,
		Hidden:               person.Hidden,
		CreatedAt:            person.CreatedAt,
		UpdatedAt:            person.UpdatedAt,
		Faces:                faces,
	}
}

func faceToResponse(face *model.Face) model.FaceResponse {
	if face == nil {
		return model.FaceResponse{}
	}
	return model.FaceResponse{
		ID:               face.ID,
		PhotoID:          face.PhotoID,
		PersonID:         face.PersonID,
		BBoxX:            face.BBoxX,
		BBoxY:            face.BBoxY,
		BBoxWidth:        face.BBoxWidth,
		BBoxHeight:       face.BBoxHeight,
		Confidence:       face.Confidence,
		QualityScore:     face.QualityScore,
		ThumbnailPath:    face.ThumbnailPath,
		ClusterStatus:    face.ClusterStatus,
		ClusterScore:     face.ClusterScore,
		ManualLocked:     face.ManualLocked,
		ManualLockReason: face.ManualLockReason,
		ManualLockedAt:   face.ManualLockedAt,
		ExclusionReason:  face.ExclusionReason,
		ExcludedAt:       face.ExcludedAt,
		UpdatedAt:        face.UpdatedAt,
	}
}

func uniquePhotoIDs(faces []*model.Face) []uint {
	seen := make(map[uint]struct{}, len(faces))
	ids := make([]uint, 0, len(faces))
	for _, face := range faces {
		if _, ok := seen[face.PhotoID]; ok {
			continue
		}
		seen[face.PhotoID] = struct{}{}
		ids = append(ids, face.PhotoID)
	}
	return ids
}

func uniquePersonIDs(faces []*model.Face) []uint {
	seen := make(map[uint]struct{}, len(faces))
	ids := make([]uint, 0, len(faces))
	for _, face := range faces {
		if face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		if _, ok := seen[*face.PersonID]; ok {
			continue
		}
		seen[*face.PersonID] = struct{}{}
		ids = append(ids, *face.PersonID)
	}
	return ids
}

func resolveThumbnailPath(cfg *config.Config, thumbnailPath string) (string, error) {
	if strings.TrimSpace(thumbnailPath) == "" {
		return "", os.ErrNotExist
	}

	fullPath := thumbnailPath
	root := ""
	if cfg != nil {
		root = strings.TrimSpace(cfg.Photos.ThumbnailPath)
	}

	if !filepath.IsAbs(fullPath) {
		if root == "" {
			return "", os.ErrNotExist
		}
		fullPath = filepath.Join(root, thumbnailPath)
	}

	fullPath = filepath.Clean(fullPath)
	if root == "" {
		return fullPath, nil
	}

	cleanRoot := filepath.Clean(root)
	rel, err := filepath.Rel(cleanRoot, fullPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", os.ErrPermission
	}

	return fullPath, nil
}

func thumbnailRoot(cfg *config.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.Photos.ThumbnailPath) != "" {
		return cfg.Photos.ThumbnailPath
	}
	return "./data/thumbnails"
}

// ==================== People Worker API Methods ====================

// GetWorkerTasks 获取人物检测任务列表（API Key认证）
func (h *PeopleHandler) GetWorkerTasks(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	workerID := c.GetHeader("X-Worker-ID")
	if workerID == "" {
		workerID = "unknown-worker"
	}

	// 获取设备信息
	_, exists := c.Get("device_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, model.Response{Success: false, Error: &model.ErrorInfo{Code: "UNAUTHORIZED", Message: "Device context missing"}})
		return
	}

	if h.runtimeService == nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INTERNAL_ERROR", Message: "People runtime service not configured"}})
		return
	}

	status, err := h.runtimeService.GetStatus(model.GlobalPeopleResourceKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INTERNAL_ERROR", Message: err.Error()}})
		return
	}
	if !status.IsActive {
		c.JSON(http.StatusConflict, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "PEOPLE_RUNTIME_NOT_ACQUIRED", Message: "People worker must acquire runtime before fetching tasks"},
			Data:    status,
		})
		return
	}
	if status.OwnerType != model.AnalysisOwnerTypePeopleWorker || status.OwnerID != workerID {
		c.JSON(http.StatusConflict, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "PEOPLE_RUNTIME_BUSY", Message: "Another people runtime is already running"},
			Data:    status,
		})
		return
	}

	// 获取待处理任务
	lockUntil := time.Now().Add(5 * time.Minute)
	jobs, err := h.jobRepo.ClaimNextRemote(workerID, limit, lockUntil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INTERNAL_ERROR", Message: err.Error()}})
		return
	}

	// 构建任务响应
	tasks := make([]model.PeopleWorkerTask, 0, len(jobs))
	for _, job := range jobs {
		photo, err := h.photoRepo.GetByID(job.PhotoID)
		if err != nil || photo == nil {
			continue
		}
		if !model.IsPhotoEligibleForPeople(photo) {
			_ = h.cancelIneligibleWorkerJob(job.ID, service.ErrPhotoPeopleExcluded)
			continue
		}

		// 检查照片是否有人工锁定的人脸
		faces, _ := h.faceRepo.ListByPhotoID(photo.ID)
		if hasManualLockedFaces(faces) {
			// 跳过此任务，释放锁
			h.jobRepo.ReleaseRemote(job.ID, workerID, "manual_locked", false)
			continue
		}

		downloadURL := fmt.Sprintf("%s/api/v1/photos/%d/image", requestBaseURL(c), photo.ID)

		tasks = append(tasks, model.PeopleWorkerTask{
			ID:            job.ID,
			JobID:         job.ID,
			PhotoID:       photo.ID,
			FilePath:      photo.FilePath,
			DownloadURL:   downloadURL,
			Width:         photo.Width,
			Height:        photo.Height,
			LockExpiresAt: job.LockExpiresAt,
		})
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    model.PeopleWorkerTasksResponse{Tasks: tasks},
	})
}

// HeartbeatWorkerTask 任务心跳（API Key认证）
func (h *PeopleHandler) HeartbeatWorkerTask(c *gin.Context) {
	taskID, err := strconv.ParseUint(c.Param("task_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_TASK_ID", Message: err.Error()}})
		return
	}

	workerID := c.GetHeader("X-Worker-ID")
	if workerID == "" {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "MISSING_WORKER_ID", Message: "X-Worker-ID header required"}})
		return
	}

	var req model.PeopleWorkerHeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}

	lockUntil := time.Now().Add(5 * time.Minute)
	if err := h.jobRepo.HeartbeatRemote(uint(taskID), workerID, req.Progress, req.StatusMessage, lockUntil); err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "HEARTBEAT_FAILED", Message: err.Error()}})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data:    model.PeopleWorkerHeartbeatResponse{LockExpiresAt: lockUntil},
	})
}

// ReleaseWorkerTask 释放任务（API Key认证）
func (h *PeopleHandler) ReleaseWorkerTask(c *gin.Context) {
	taskID, err := strconv.ParseUint(c.Param("task_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_TASK_ID", Message: err.Error()}})
		return
	}

	workerID := c.GetHeader("X-Worker-ID")
	if workerID == "" {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "MISSING_WORKER_ID", Message: "X-Worker-ID header required"}})
		return
	}

	var req model.PeopleWorkerReleaseTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}

	if err := h.jobRepo.ReleaseRemote(uint(taskID), workerID, req.Reason, req.RetryLater); err != nil {
		c.JSON(http.StatusConflict, model.Response{Success: false, Error: &model.ErrorInfo{Code: "RELEASE_FAILED", Message: err.Error()}})
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "Task released"})
}

// SubmitWorkerResults 提交检测结果（API Key认证）
func (h *PeopleHandler) SubmitWorkerResults(c *gin.Context) {
	workerID := c.GetHeader("X-Worker-ID")
	if workerID == "" {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "MISSING_WORKER_ID", Message: "X-Worker-ID header required"}})
		return
	}

	var req model.PeopleWorkerSubmitResultsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}

	processed := 0
	errors := make([]string, 0)

	for _, result := range req.Results {
		// 获取任务
		job, err := h.jobRepo.GetByID(result.TaskID)
		if err != nil || job == nil {
			errors = append(errors, fmt.Sprintf("task %d not found", result.TaskID))
			continue
		}

		// 验证 worker 拥有此任务
		if job.WorkerID != workerID {
			errors = append(errors, fmt.Sprintf("task %d not owned by this worker", result.TaskID))
			continue
		}

		// 获取照片
		photo, err := h.photoRepo.GetByID(result.PhotoID)
		if err != nil || photo == nil {
			errors = append(errors, fmt.Sprintf("photo %d not found", result.PhotoID))
			continue
		}
		if !model.IsPhotoEligibleForPeople(photo) {
			_ = h.cancelIneligibleWorkerJob(job.ID, service.ErrPhotoPeopleExcluded)
			errors = append(errors, fmt.Sprintf("task %d: %v", result.TaskID, service.ErrPhotoPeopleExcluded))
			continue
		}

		// 应用检测结果
		if err := h.service.ApplyDetectionResult(job, photo, &result); err != nil {
			errors = append(errors, fmt.Sprintf("task %d: %v", result.TaskID, err))
			continue
		}

		processed++
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.PeopleWorkerSubmitResultsResponse{
			Processed: processed,
			Errors:    errors,
		},
	})
}

func (h *PeopleHandler) cancelIneligibleWorkerJob(jobID uint, reason error) error {
	now := time.Now()
	return h.jobRepo.UpdateFields(jobID, map[string]interface{}{
		"status":            model.PeopleJobStatusCancelled,
		"worker_id":         "",
		"lock_expires_at":   nil,
		"last_heartbeat_at": nil,
		"last_error":        reason.Error(),
		"status_message":    reason.Error(),
		"completed_at":      &now,
	})
}

// AcquirePeopleRuntime 获取人物运行时租约（API Key认证）
func (h *PeopleHandler) AcquirePeopleRuntime(c *gin.Context) {
	var req model.PeopleWorkerRuntimeLeaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}
	if strings.TrimSpace(req.WorkerID) == "" {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "worker_id is required"}})
		return
	}
	if h.runtimeService == nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INTERNAL_ERROR", Message: "People runtime service not configured"}})
		return
	}

	lease, err := h.runtimeService.Acquire(
		model.GlobalPeopleResourceKey,
		model.AnalysisOwnerTypePeopleWorker,
		req.WorkerID,
		"people worker runtime acquired",
	)
	if err != nil {
		if errors.Is(err, service.ErrAnalysisRuntimeBusy) {
			status, _ := h.runtimeService.GetStatus(model.GlobalPeopleResourceKey)
			c.JSON(http.StatusConflict, model.Response{
				Success: false,
				Error:   &model.ErrorInfo{Code: "PEOPLE_RUNTIME_BUSY", Message: "Another people runtime is already running"},
				Data:    status,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INTERNAL_ERROR", Message: err.Error()}})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "People runtime acquired",
		Data: model.PeopleWorkerRuntimeLeaseResponse{
			LeaseExpiresAt: *lease.LeaseExpiresAt,
		},
	})
}

// HeartbeatPeopleRuntime 续约人物运行时租约（API Key认证）
func (h *PeopleHandler) HeartbeatPeopleRuntime(c *gin.Context) {
	var req model.PeopleWorkerRuntimeLeaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}
	if strings.TrimSpace(req.WorkerID) == "" {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "worker_id is required"}})
		return
	}
	if h.runtimeService == nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INTERNAL_ERROR", Message: "People runtime service not configured"}})
		return
	}

	lease, err := h.runtimeService.Heartbeat(model.GlobalPeopleResourceKey, model.AnalysisOwnerTypePeopleWorker, req.WorkerID)
	if err != nil {
		status, _ := h.runtimeService.GetStatus(model.GlobalPeopleResourceKey)
		c.JSON(http.StatusConflict, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "PEOPLE_RUNTIME_OWNED_BY_OTHER", Message: err.Error()},
			Data:    status,
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "People runtime heartbeat updated",
		Data: model.PeopleWorkerRuntimeLeaseResponse{
			LeaseExpiresAt: *lease.LeaseExpiresAt,
		},
	})
}

// ReleasePeopleRuntime 释放人物运行时租约（API Key认证）
func (h *PeopleHandler) ReleasePeopleRuntime(c *gin.Context) {
	var req model.PeopleWorkerRuntimeLeaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: err.Error()}})
		return
	}
	if strings.TrimSpace(req.WorkerID) == "" {
		c.JSON(http.StatusBadRequest, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INVALID_REQUEST", Message: "worker_id is required"}})
		return
	}
	if h.runtimeService == nil {
		c.JSON(http.StatusInternalServerError, model.Response{Success: false, Error: &model.ErrorInfo{Code: "INTERNAL_ERROR", Message: "People runtime service not configured"}})
		return
	}

	if err := h.runtimeService.Release(model.GlobalPeopleResourceKey, model.AnalysisOwnerTypePeopleWorker, req.WorkerID); err != nil {
		status, _ := h.runtimeService.GetStatus(model.GlobalPeopleResourceKey)
		c.JSON(http.StatusConflict, model.Response{
			Success: false,
			Error:   &model.ErrorInfo{Code: "PEOPLE_RUNTIME_OWNED_BY_OTHER", Message: err.Error()},
			Data:    status,
		})
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "People runtime released"})
}

// hasManualLockedFaces 检查是否有人工锁定的人脸
func hasManualLockedFaces(faces []*model.Face) bool {
	for _, face := range faces {
		if face != nil && face.ManualLocked {
			return true
		}
	}
	return false
}

// ==================== Identity Profile operational stats (Task 14) ====================

// identityProfileDecisionsDefaultLimit / Max 是 decisions 查询的默认/上限 limit。
const (
	identityProfileDecisionsDefaultLimit = 50
	identityProfileDecisionsMaxLimit     = 200
)

// GetIdentityProfileStats 返回身份画像只读运行状态。
// legacy 模式仅返回 mode 与零值运行状态，不访问 Repository/ANN/AppConfig/decision 仓库。
// 统计失败返回 500 + IDENTITY_PROFILE_STATS_FAILED，不暴露原始 SQLite 错误或路径。
func (h *PeopleHandler) GetIdentityProfileStats(c *gin.Context) {
	if h.identityProfileService == nil {
		writePeopleError(c, http.StatusInternalServerError, "IDENTITY_PROFILE_STATS_FAILED", "identity profile service not configured")
		return
	}
	stats, err := h.identityProfileService.GetOperationalStats(h.identityDecisionRepo)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "IDENTITY_PROFILE_STATS_FAILED", "identity profile stats unavailable")
		return
	}
	c.JSON(http.StatusOK, model.Response{Success: true, Data: stats})
}

// ListIdentityProfileDecisions 返回最近 limit 条身份画像决策遥测（只读 DTO）。
// limit 规则：未传 50；小于 1 返回 400；大于 200 截断为 200；非整数返回 400。
// 不返回 Face ID、hash、decision key、embedding 或路径。
func (h *PeopleHandler) ListIdentityProfileDecisions(c *gin.Context) {
	if h.identityProfileService == nil {
		writePeopleError(c, http.StatusInternalServerError, "IDENTITY_DECISIONS_FAILED", "identity profile service not configured")
		return
	}

	limit := identityProfileDecisionsDefaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			writePeopleError(c, http.StatusBadRequest, "INVALID_LIMIT", "limit must be a positive integer between 1 and 200")
			return
		}
		limit = v
	}
	if limit > identityProfileDecisionsMaxLimit {
		limit = identityProfileDecisionsMaxLimit
	}

	items, err := h.identityProfileService.ListRecentDecisions(limit, h.identityDecisionRepo)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "IDENTITY_DECISIONS_FAILED", "identity decisions unavailable")
		return
	}
	if items == nil {
		items = []model.IdentityDecisionResponse{}
	}
	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.IdentityDecisionListResponse{
			Items: items,
			Limit: limit,
		},
	})
}
