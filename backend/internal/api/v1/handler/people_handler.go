package handler

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type PeopleHandler struct {
	service    service.PeopleService
	personRepo repository.PersonRepository
	faceRepo   repository.FaceRepository
	photoRepo  repository.PhotoRepository
	cfg        *config.Config
}

func NewPeopleHandler(service service.PeopleService, personRepo repository.PersonRepository, faceRepo repository.FaceRepository, photoRepo repository.PhotoRepository, cfg *config.Config) *PeopleHandler {
	return &PeopleHandler{
		service:    service,
		personRepo: personRepo,
		faceRepo:   faceRepo,
		photoRepo:  photoRepo,
		cfg:        cfg,
	}
}

func (h *PeopleHandler) ListPeople(c *gin.Context) {
	page, pageSize, ok := parsePagination(c)
	if !ok {
		return
	}

	people, err := h.personRepo.ListAll()
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

	category := strings.TrimSpace(c.Query("category"))
	search := strings.ToLower(strings.TrimSpace(c.Query("search")))

	filtered := make([]*model.Person, 0, len(people))
	for _, person := range people {
		if category != "" && person.Category != category {
			continue
		}
		if search != "" {
			searchText := strings.ToLower(person.Name + " " + person.Category + " " + strconv.FormatUint(uint64(person.ID), 10))
			if !strings.Contains(searchText, search) {
				continue
			}
		}
		filtered = append(filtered, person)
	}

	sort.Slice(filtered, func(i, j int) bool {
		left := filtered[i]
		right := filtered[j]
		if personCategoryOrder(left.Category) != personCategoryOrder(right.Category) {
			return personCategoryOrder(left.Category) < personCategoryOrder(right.Category)
		}
		if left.PhotoCount != right.PhotoCount {
			return left.PhotoCount > right.PhotoCount
		}
		if left.FaceCount != right.FaceCount {
			return left.FaceCount > right.FaceCount
		}
		return left.ID < right.ID
	})

	total := len(filtered)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	items := make([]model.PersonResponse, 0, end-start)
	for _, person := range filtered[start:end] {
		items = append(items, personToResponse(person, nil))
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.PagedResponse{
			Items:      items,
			Total:      int64(total),
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

	if !h.ensurePersonExists(c, personID) {
		return
	}

	faces, err := h.faceRepo.ListByPersonID(personID)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	photoIDs := uniquePhotoIDs(faces)
	photos, err := h.photoRepo.ListByIDs(photoIDs)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	sort.Slice(photos, func(i, j int) bool {
		leftTime := photoSortUnix(photos[i])
		rightTime := photoSortUnix(photos[j])
		if leftTime != rightTime {
			return leftTime > rightTime
		}
		return photos[i].ID > photos[j].ID
	})

	c.JSON(http.StatusOK, model.Response{Success: true, Data: photos})
}

func (h *PeopleHandler) GetPersonFaces(c *gin.Context) {
	personID, ok := parseUintParam(c, "id", "Invalid person ID")
	if !ok {
		return
	}

	if !h.ensurePersonExists(c, personID) {
		return
	}

	faces, err := h.faceRepo.ListByPersonID(personID)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	sort.Slice(faces, func(i, j int) bool {
		if faces[i].QualityScore != faces[j].QualityScore {
			return faces[i].QualityScore > faces[j].QualityScore
		}
		return faces[i].ID < faces[j].ID
	})

	resp := make([]model.FaceResponse, 0, len(faces))
	for _, face := range faces {
		resp = append(resp, faceToResponse(face))
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Data: resp})
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

func (h *PeopleHandler) MergePeople(c *gin.Context) {
	var req model.MergePeopleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.service.MergePeople(req.TargetPersonID, req.SourcePersonIDs); err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人物已合并"})
}

func (h *PeopleHandler) SplitPerson(c *gin.Context) {
	var req model.SplitPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	person, err := h.service.SplitPerson(req.FaceIDs)
	if err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: "人物已拆分",
		Data:    personToResponse(person, nil),
	})
}

func (h *PeopleHandler) MoveFaces(c *gin.Context) {
	var req model.MoveFacesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	if err := h.service.MoveFaces(req.FaceIDs, req.TargetPersonID); err != nil {
		writeServiceFailure(c, err)
		return
	}

	c.JSON(http.StatusOK, model.Response{Success: true, Message: "人脸已移动"})
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

func (h *PeopleHandler) RescanByPath(c *gin.Context) {
	var req model.PeopleBatchEnqueueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writePeopleError(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	backgroundStarted := false
	task := h.service.GetTaskStatus()
	if task != nil && task.Status == model.TaskStatusStopping {
		writePeopleError(c, http.StatusConflict, "START_FAILED", "人物后台任务正在停止中，请稍后重试")
		return
	}
	if task == nil || task.Status == model.TaskStatusStopped {
		if _, err := h.service.StartBackground(); err != nil {
			writePeopleError(c, http.StatusConflict, "START_FAILED", err.Error())
			return
		}
		backgroundStarted = true
	}

	count, err := h.service.EnqueueByPath(req.Path, model.PeopleJobSourceManual, 80)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "ENQUEUE_FAILED", err.Error())
		return
	}

	message := "人物重扫任务已加入队列"
	if backgroundStarted {
		message = "人物后台任务已启动，并已加入重扫队列"
	}

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Message: message,
		Data: gin.H{
			"count":              count,
			"background_started": backgroundStarted,
		},
	})
}

func (h *PeopleHandler) GetPhotoPeople(c *gin.Context) {
	photoID, ok := parseUintParam(c, "id", "Invalid photo ID")
	if !ok {
		return
	}

	photo, err := h.photoRepo.GetByID(photoID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		writePeopleError(c, status, "NOT_FOUND", "Photo not found")
		return
	}

	faces, err := h.faceRepo.ListByPhotoID(photoID)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	personIDs := uniquePersonIDs(faces)
	people, err := h.personRepo.ListByIDs(personIDs)
	if err != nil {
		writePeopleError(c, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	facesByPerson := make(map[uint][]model.FaceResponse, len(personIDs))
	for _, face := range faces {
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

	c.JSON(http.StatusOK, model.Response{
		Success: true,
		Data: model.PhotoPersonResponse{
			PhotoID:           photo.ID,
			FaceProcessStatus: photo.FaceProcessStatus,
			FaceCount:         photo.FaceCount,
			TopPersonCategory: photo.TopPersonCategory,
			People:            respPeople,
		},
	})
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
		thumbnailPath, genErr := util.GenerateFaceThumbnail(photo.FilePath, thumbnailRoot(h.cfg), face.BBoxX, face.BBoxY, face.BBoxWidth, face.BBoxHeight)
		if genErr != nil {
			writePeopleError(c, http.StatusInternalServerError, "GENERATE_FAILED", genErr.Error())
			return
		}
		if updateErr := h.faceRepo.UpdateFields(face.ID, map[string]interface{}{"thumbnail_path": thumbnailPath}); updateErr != nil {
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
	c.Header("Cache-Control", "private, max-age=3600")
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
		AvatarLocked:         person.AvatarLocked,
		FaceCount:            person.FaceCount,
		PhotoCount:           person.PhotoCount,
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
		ManualLocked:     face.ManualLocked,
		ManualLockReason: face.ManualLockReason,
		ManualLockedAt:   face.ManualLockedAt,
	}
}

func personCategoryOrder(category string) int {
	switch category {
	case model.PersonCategoryFamily:
		return 0
	case model.PersonCategoryFriend:
		return 1
	case model.PersonCategoryAcquaintance:
		return 2
	case model.PersonCategoryStranger:
		return 3
	default:
		return 4
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

func photoSortUnix(photo *model.Photo) int64 {
	if photo == nil || photo.TakenAt == nil {
		return 0
	}
	return photo.TakenAt.Unix()
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
