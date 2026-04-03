package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/mlclient"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

const (
	peoplePriorityScan     = 50
	peoplePriorityManual   = 80
	peoplePriorityPassive  = 100
	peopleClusterThreshold = 0.88
	peoplePrototypeCount   = 3
	peopleLinkThreshold    = 0.72
	peopleAttachThreshold  = 0.86
	peopleMinClusterFaces  = 2
)

type PeopleMLClient interface {
	DetectFaces(ctx context.Context, request mlclient.DetectFacesRequest) (*mlclient.DetectFacesResponse, error)
}

type PeopleService interface {
	StartBackground() (*model.PeopleTask, error)
	StopBackground() error
	GetTaskStatus() *model.PeopleTask
	GetStats() (*model.PeopleStatsResponse, error)
	GetBackgroundLogs() []string
	EnqueuePhoto(photoID uint, source string, priority int, force bool) error
	EnqueueByPath(path string, source string, priority int) (int, error)
	MergePeople(targetPersonID uint, sourcePersonIDs []uint) error
	SplitPerson(faceIDs []uint) (*model.Person, error)
	MoveFaces(faceIDs []uint, targetPersonID uint) error
	UpdatePersonCategory(personID uint, category string) error
	UpdatePersonName(personID uint, name string) error
	UpdatePersonAvatar(personID uint, faceID uint) error
	HandleShutdown() error
}

type peopleService struct {
	db         *gorm.DB
	photoRepo  repository.PhotoRepository
	faceRepo   repository.FaceRepository
	personRepo repository.PersonRepository
	jobRepo    repository.PeopleJobRepository
	config     *config.Config
	client     PeopleMLClient

	taskMutex       sync.RWMutex
	task            *model.PeopleTask
	active          *activePeopleTask
	backgroundLogMu sync.RWMutex
	backgroundLogs  []string
}

type activePeopleTask struct {
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	stop   bool
}

func NewPeopleService(db *gorm.DB, photoRepo repository.PhotoRepository, faceRepo repository.FaceRepository, personRepo repository.PersonRepository, jobRepo repository.PeopleJobRepository, cfg *config.Config, client PeopleMLClient) PeopleService {
	return &peopleService{
		db:         db,
		photoRepo:  photoRepo,
		faceRepo:   faceRepo,
		personRepo: personRepo,
		jobRepo:    jobRepo,
		config:     cfg,
		client:     client,
	}
}

func (s *peopleService) StartBackground() (*model.PeopleTask, error) {
	if s.client == nil {
		return nil, fmt.Errorf("people ml client not configured")
	}
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.active != nil {
		return nil, fmt.Errorf("people task already running")
	}

	now := time.Now()
	task := &model.PeopleTask{
		Status:    model.TaskStatusRunning,
		StartedAt: &now,
	}
	active := &activePeopleTask{
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	s.task = task
	s.active = active
	s.resetBackgroundLogs()
	s.appendBackgroundLog("人物后台任务已启动")
	go s.runBackground(active)
	return clonePeopleTask(task), nil
}

func (s *peopleService) StopBackground() error {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.active == nil {
		return fmt.Errorf("people task not running")
	}
	s.active.mu.Lock()
	if !s.active.stop {
		s.active.stop = true
		close(s.active.stopCh)
	}
	s.active.mu.Unlock()
	if s.task != nil && s.task.Status == model.TaskStatusRunning {
		s.task.Status = model.TaskStatusStopping
		s.appendBackgroundLog("收到停止请求，等待当前人物任务处理完成")
	}
	return nil
}

func (s *peopleService) GetTaskStatus() *model.PeopleTask {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()
	return clonePeopleTask(s.task)
}

func (s *peopleService) GetStats() (*model.PeopleStatsResponse, error) {
	stats, err := s.jobRepo.GetStats()
	if err != nil {
		return nil, err
	}
	return &model.PeopleStatsResponse{
		Total:      stats.Total,
		Pending:    stats.Pending,
		Queued:     stats.Queued,
		Processing: stats.Processing,
		Completed:  stats.Completed,
		Failed:     stats.Failed,
		Cancelled:  stats.Cancelled,
	}, nil
}

func (s *peopleService) GetBackgroundLogs() []string {
	s.backgroundLogMu.RLock()
	defer s.backgroundLogMu.RUnlock()
	logs := make([]string, len(s.backgroundLogs))
	copy(logs, s.backgroundLogs)
	return logs
}

func (s *peopleService) EnqueuePhoto(photoID uint, source string, priority int, force bool) error {
	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		return err
	}
	return s.enqueuePhotoModel(photo, source, priority, force)
}

func (s *peopleService) EnqueueByPath(path string, source string, priority int) (int, error) {
	photos, err := s.photoRepo.ListByPathPrefix(path)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, photo := range photos {
		if photo.Status == model.PhotoStatusExcluded {
			continue
		}
		if err := s.enqueuePhotoModel(photo, source, priority, false); err != nil {
			logger.Warnf("enqueue people by path failed for photo %d: %v", photo.ID, err)
			continue
		}
		count++
	}

	return count, nil
}

func (s *peopleService) HandleShutdown() error {
	s.taskMutex.RLock()
	active := s.active
	s.taskMutex.RUnlock()
	if active == nil {
		return nil
	}
	return s.StopBackground()
}

func (s *peopleService) MergePeople(targetPersonID uint, sourcePersonIDs []uint) error {
	affectedPhotoIDs, err := s.personRepo.MergeInto(targetPersonID, sourcePersonIDs)
	if err != nil {
		return err
	}
	if err := s.syncPersonState(targetPersonID); err != nil {
		return err
	}
	return s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs)
}

func (s *peopleService) SplitPerson(faceIDs []uint) (*model.Person, error) {
	faces, err := s.faceRepo.ListByIDs(faceIDs)
	if err != nil {
		return nil, err
	}
	if len(faces) == 0 {
		return nil, fmt.Errorf("faces not found")
	}

	var sourcePersonID uint
	for _, face := range faces {
		if face.PersonID == nil || *face.PersonID == 0 {
			return nil, fmt.Errorf("face %d has no person", face.ID)
		}
		if sourcePersonID == 0 {
			sourcePersonID = *face.PersonID
			continue
		}
		if sourcePersonID != *face.PersonID {
			return nil, fmt.Errorf("split faces must belong to the same person")
		}
	}

	sourcePerson, err := s.personRepo.GetByID(sourcePersonID)
	if err != nil {
		return nil, err
	}
	if sourcePerson == nil {
		return nil, fmt.Errorf("source person not found")
	}

	newPerson := &model.Person{Category: sourcePerson.Category}
	if err := s.personRepo.Create(newPerson); err != nil {
		return nil, err
	}
	if err := s.faceRepo.ReassignFaces(faceIDs, newPerson.ID, "split"); err != nil {
		return nil, err
	}

	if err := s.syncPersonState(sourcePersonID); err != nil {
		return nil, err
	}
	if err := s.syncPersonState(newPerson.ID); err != nil {
		return nil, err
	}
	if err := s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces)); err != nil {
		return nil, err
	}

	return s.personRepo.GetByID(newPerson.ID)
}

func (s *peopleService) MoveFaces(faceIDs []uint, targetPersonID uint) error {
	faces, err := s.faceRepo.ListByIDs(faceIDs)
	if err != nil {
		return err
	}
	if len(faces) == 0 {
		return fmt.Errorf("faces not found")
	}

	sourcePersonIDs := make(map[uint]struct{})
	for _, face := range faces {
		if face.PersonID != nil && *face.PersonID != 0 && *face.PersonID != targetPersonID {
			sourcePersonIDs[*face.PersonID] = struct{}{}
		}
	}

	if err := s.faceRepo.ReassignFaces(faceIDs, targetPersonID, "move"); err != nil {
		return err
	}

	if err := s.syncPersonState(targetPersonID); err != nil {
		return err
	}
	for personID := range sourcePersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return err
		}
	}

	return s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces))
}

func (s *peopleService) UpdatePersonCategory(personID uint, category string) error {
	if err := s.personRepo.UpdateFields(personID, map[string]interface{}{"category": category}); err != nil {
		return err
	}
	faces, err := s.faceRepo.ListByPersonID(personID)
	if err != nil {
		return err
	}
	return s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces))
}

func (s *peopleService) UpdatePersonName(personID uint, name string) error {
	return s.personRepo.UpdateFields(personID, map[string]interface{}{"name": name})
}

func (s *peopleService) UpdatePersonAvatar(personID uint, faceID uint) error {
	face, err := s.faceRepo.GetByID(faceID)
	if err != nil {
		return err
	}
	if face == nil || face.PersonID == nil || *face.PersonID != personID {
		return fmt.Errorf("face %d does not belong to person %d", faceID, personID)
	}
	return s.personRepo.UpdateFields(personID, map[string]interface{}{
		"representative_face_id": faceID,
		"avatar_locked":          true,
	})
}

func (s *peopleService) enqueuePhotoModel(photo *model.Photo, source string, priority int, force bool) error {
	if photo == nil {
		return fmt.Errorf("photo is nil")
	}
	if photo.Status == model.PhotoStatusExcluded {
		return nil
	}
	if source == "" {
		source = model.PeopleJobSourceManual
	}
	if priority <= 0 {
		priority = peoplePriorityManual
	}

	now := time.Now()
	if err := s.photoRepo.UpdateFields(photo.ID, map[string]interface{}{
		"face_process_status": model.FaceProcessStatusPending,
	}); err != nil {
		return err
	}

	activeJob, err := s.jobRepo.GetActiveByPhotoID(photo.ID)
	if err != nil {
		return err
	}
	if activeJob != nil {
		updates := map[string]interface{}{
			"priority":          priority,
			"source":            source,
			"last_requested_at": &now,
		}
		if force || activeJob.Status == model.PeopleJobStatusPending {
			updates["status"] = model.PeopleJobStatusQueued
		}
		return s.jobRepo.UpdateFields(activeJob.ID, updates)
	}

	job := &model.PeopleJob{
		PhotoID:         photo.ID,
		FilePath:        photo.FilePath,
		Status:          model.PeopleJobStatusQueued,
		Priority:        priority,
		Source:          source,
		QueuedAt:        now,
		LastRequestedAt: &now,
	}
	return s.jobRepo.Create(job)
}

func (s *peopleService) runBackground(active *activePeopleTask) {
	defer func() {
		now := time.Now()
		s.taskMutex.Lock()
		if s.task != nil && (s.task.Status == model.TaskStatusRunning || s.task.Status == model.TaskStatusStopping) {
			s.task.Status = model.TaskStatusStopped
			s.task.StoppedAt = &now
		}
		s.appendBackgroundLog("人物后台任务已停止")
		s.active = nil
		s.taskMutex.Unlock()
		close(active.done)
	}()

	for {
		active.mu.Lock()
		stopRequested := active.stop
		active.mu.Unlock()
		if stopRequested {
			return
		}

		job, err := s.jobRepo.ClaimNextJob()
		if err != nil {
			s.appendBackgroundLog(fmt.Sprintf("领取人物任务失败：%v", err))
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if job == nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		if err := s.processJob(job); err != nil {
			s.appendBackgroundLog(fmt.Sprintf("处理人物任务 %d 失败：%v", job.ID, err))
		}

		s.taskMutex.Lock()
		if s.task != nil {
			s.task.CurrentPhotoID = job.PhotoID
			s.task.ProcessedJobs++
		}
		s.taskMutex.Unlock()
	}
}

func (s *peopleService) processJob(job *model.PeopleJob) error {
	photo, err := s.photoRepo.GetByID(job.PhotoID)
	if err != nil {
		return err
	}

	now := time.Now()
	if photo == nil || photo.Status == model.PhotoStatusExcluded {
		return s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusCancelled,
			"completed_at": &now,
		})
	}

	existingFaces, err := s.faceRepo.ListByPhotoID(photo.ID)
	if err != nil {
		return err
	}
	if hasManualLockedFaces(existingFaces) {
		if err := s.photoRepo.RecomputeTopPersonCategory([]uint{photo.ID}); err != nil {
			return err
		}
		return s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusCompleted,
			"last_error":   "",
			"completed_at": &now,
		})
	}

	if err := s.photoRepo.UpdateFields(photo.ID, map[string]interface{}{
		"face_process_status": model.FaceProcessStatusProcessing,
	}); err != nil {
		return err
	}

	processor := util.NewImageProcessor(1024, 85)
	processedImage, processErr := processor.ProcessForAI(photo.FilePath)
	if processErr != nil {
		logger.Warnf("process photo %d for people detect failed, falling back to image path: %v", photo.ID, processErr)
	}

	var imageBase64 string
	if len(processedImage) > 0 {
		imageBase64 = base64.StdEncoding.EncodeToString(processedImage)
	}

	result, detectErr := s.client.DetectFaces(context.Background(), mlclient.DetectFacesRequest{
		ImagePath:     photo.FilePath,
		ImageBase64:   imageBase64,
		MinConfidence: 0.5,
		MaxFaces:      20,
	})
	if detectErr != nil {
		if updateErr := s.photoRepo.UpdateFields(photo.ID, map[string]interface{}{
			"face_process_status": model.FaceProcessStatusFailed,
		}); updateErr != nil {
			logger.Warnf("update photo %d failed status after people detect error failed: %v", photo.ID, updateErr)
		}
		return s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusFailed,
			"last_error":   detectErr.Error(),
			"completed_at": &now,
		})
	}

	if result == nil {
		result = &mlclient.DetectFacesResponse{}
	}

	if len(result.Faces) == 0 {
		return s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("photo_id = ?", photo.ID).Delete(&model.Face{}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
				"face_process_status": model.FaceProcessStatusNoFace,
				"face_count":          0,
				"top_person_category": "",
			}).Error; err != nil {
				return err
			}
			return tx.Model(&model.PeopleJob{}).Where("id = ?", job.ID).Updates(map[string]interface{}{
				"status":       model.PeopleJobStatusCompleted,
				"last_error":   "",
				"completed_at": &now,
			}).Error
		})
	}

	previousPersonIDs := personIDsFromFaces(existingFaces)
	createdFaces := make([]*model.Face, 0, len(result.Faces))
	for _, detected := range result.Faces {
		embeddingPayload, err := json.Marshal(detected.Embedding)
		if err != nil {
			return err
		}
		thumbnailPath, err := s.generateFaceThumbnail(photo, detected.BBox)
		if err != nil {
			return err
		}
		face := &model.Face{
			PhotoID:       photo.ID,
			BBoxX:         detected.BBox.X,
			BBoxY:         detected.BBox.Y,
			BBoxWidth:     detected.BBox.Width,
			BBoxHeight:    detected.BBox.Height,
			Confidence:    detected.Confidence,
			QualityScore:  detected.QualityScore,
			Embedding:     embeddingPayload,
			ThumbnailPath: thumbnailPath,
			ClusterStatus: model.FaceClusterStatusPending,
			ClusterScore:  0,
			ClusteredAt:   nil,
		}
		createdFaces = append(createdFaces, face)
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("photo_id = ?", photo.ID).Delete(&model.Face{}).Error; err != nil {
			return err
		}

		for _, face := range createdFaces {
			if err := tx.Create(face).Error; err != nil {
				return err
			}
		}

		if err := tx.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
			"face_process_status": model.FaceProcessStatusReady,
			"face_count":          len(createdFaces),
			"top_person_category": "",
		}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	affectedPersonIDs, affectedPhotoIDs, clusterErr := s.runIncrementalClustering()
	if clusterErr != nil {
		if updateErr := s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusFailed,
			"last_error":   clusterErr.Error(),
			"completed_at": &now,
		}); updateErr != nil {
			logger.Warnf("update people job %d failed after clustering error: %v", job.ID, updateErr)
		}
		return clusterErr
	}

	for _, personID := range previousPersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return err
		}
	}
	for _, personID := range affectedPersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return err
		}
	}

	affectedPhotoIDs = append(affectedPhotoIDs, photo.ID)
	if err := s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs); err != nil {
		return err
	}

	return s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
		"status":       model.PeopleJobStatusCompleted,
		"last_error":   "",
		"completed_at": &now,
	})
}

func (s *peopleService) resetBackgroundLogs() {
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()
	s.backgroundLogs = nil
}

func (s *peopleService) appendBackgroundLog(message string) {
	entry := fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), message)
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()
	s.backgroundLogs = append(s.backgroundLogs, entry)
	if len(s.backgroundLogs) > 100 {
		s.backgroundLogs = s.backgroundLogs[len(s.backgroundLogs)-100:]
	}
}

func (s *peopleService) generateFaceThumbnail(photo *model.Photo, bbox mlclient.BoundingBox) (string, error) {
	if photo == nil {
		return "", fmt.Errorf("photo is nil")
	}
	return util.GenerateFaceThumbnail(photo.FilePath, s.faceThumbnailRoot(), bbox.X, bbox.Y, bbox.Width, bbox.Height)
}

func (s *peopleService) faceThumbnailRoot() string {
	if s.config != nil && strings.TrimSpace(s.config.Photos.ThumbnailPath) != "" {
		return s.config.Photos.ThumbnailPath
	}
	return "./data/thumbnails"
}

func clonePeopleTask(task *model.PeopleTask) *model.PeopleTask {
	if task == nil {
		return nil
	}
	clone := *task
	return &clone
}

func (s *peopleService) ensurePersonForDetectedFace(detected mlclient.DetectedFace, candidates []*model.Face, people map[uint]*model.Person) (uint, error) {
	bestPersonID := uint(0)
	bestScore := -1.0

	for _, face := range candidates {
		if face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		score := cosineSimilarity(detected.Embedding, decodeEmbedding(face.Embedding))
		if score > bestScore {
			bestScore = score
			bestPersonID = *face.PersonID
		}
	}

	if bestPersonID != 0 && bestScore >= peopleClusterThreshold {
		if _, ok := people[bestPersonID]; ok {
			return bestPersonID, nil
		}
	}

	person := &model.Person{Category: model.PersonCategoryStranger}
	if err := s.personRepo.Create(person); err != nil {
		return 0, err
	}
	people[person.ID] = person
	return person.ID, nil
}

func (s *peopleService) selectPersonPrototypes(faces []*model.Face, k int) map[uint][]*model.Face {
	prototypes := make(map[uint][]*model.Face)
	if k <= 0 {
		return prototypes
	}

	grouped := make(map[uint][]*model.Face)
	for _, face := range faces {
		if face == nil || face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		personID := *face.PersonID
		grouped[personID] = append(grouped[personID], face)
	}

	for personID, personFaces := range grouped {
		sort.Slice(personFaces, func(i, j int) bool {
			if personFaces[i].ManualLocked != personFaces[j].ManualLocked {
				return personFaces[i].ManualLocked
			}
			if personFaces[i].QualityScore != personFaces[j].QualityScore {
				return personFaces[i].QualityScore > personFaces[j].QualityScore
			}
			if personFaces[i].Confidence != personFaces[j].Confidence {
				return personFaces[i].Confidence > personFaces[j].Confidence
			}
			return personFaces[i].ID < personFaces[j].ID
		})

		if len(personFaces) > k {
			personFaces = personFaces[:k]
		}
		prototypes[personID] = personFaces
	}

	return prototypes
}

func (s *peopleService) buildFaceGraph(faces []*model.Face, linkThreshold float64) map[uint][]uint {
	graph := make(map[uint][]uint, len(faces))
	for _, face := range faces {
		if face == nil || face.ID == 0 {
			continue
		}
		graph[face.ID] = []uint{}
	}

	for i := 0; i < len(faces); i++ {
		if faces[i] == nil || faces[i].ID == 0 {
			continue
		}
		for j := i + 1; j < len(faces); j++ {
			if faces[j] == nil || faces[j].ID == 0 {
				continue
			}

			score := cosineSimilarity(decodeEmbedding(faces[i].Embedding), decodeEmbedding(faces[j].Embedding))
			if score < linkThreshold {
				continue
			}

			graph[faces[i].ID] = append(graph[faces[i].ID], faces[j].ID)
			graph[faces[j].ID] = append(graph[faces[j].ID], faces[i].ID)
		}
	}

	for faceID := range graph {
		sort.Slice(graph[faceID], func(i, j int) bool {
			return graph[faceID][i] < graph[faceID][j]
		})
	}

	return graph
}

func (s *peopleService) findConnectedComponents(graph map[uint][]uint) [][]uint {
	if len(graph) == 0 {
		return nil
	}

	nodeIDs := make([]uint, 0, len(graph))
	for faceID := range graph {
		nodeIDs = append(nodeIDs, faceID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })

	visited := make(map[uint]bool, len(graph))
	components := make([][]uint, 0)

	for _, startID := range nodeIDs {
		if visited[startID] {
			continue
		}

		queue := []uint{startID}
		visited[startID] = true
		component := make([]uint, 0)

		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			component = append(component, current)

			for _, neighbor := range graph[current] {
				if visited[neighbor] {
					continue
				}
				visited[neighbor] = true
				queue = append(queue, neighbor)
			}
		}

		sort.Slice(component, func(i, j int) bool { return component[i] < component[j] })
		components = append(components, component)
	}

	return components
}

func (s *peopleService) scoreComponentAgainstPerson(component []*model.Face, prototypes []*model.Face) float64 {
	if len(component) == 0 || len(prototypes) == 0 {
		return -1
	}

	var total float64
	var scored int

	for _, face := range component {
		if face == nil {
			continue
		}

		bestScore := -1.0
		embedding := decodeEmbedding(face.Embedding)
		for _, prototype := range prototypes {
			if prototype == nil {
				continue
			}
			score := cosineSimilarity(embedding, decodeEmbedding(prototype.Embedding))
			if score > bestScore {
				bestScore = score
			}
		}

		if bestScore < 0 {
			continue
		}
		total += bestScore
		scored++
	}

	if scored == 0 {
		return -1
	}
	return total / float64(scored)
}

func (s *peopleService) attachComponentToExistingPerson(component []*model.Face, prototypes map[uint][]*model.Face, attachThreshold float64) (uint, float64, bool) {
	if len(component) == 0 || len(prototypes) == 0 {
		return 0, -1, false
	}

	personIDs := make([]uint, 0, len(prototypes))
	for personID := range prototypes {
		personIDs = append(personIDs, personID)
	}
	sort.Slice(personIDs, func(i, j int) bool { return personIDs[i] < personIDs[j] })

	bestPersonID := uint(0)
	bestScore := -1.0
	for _, personID := range personIDs {
		score := s.scoreComponentAgainstPerson(component, prototypes[personID])
		if score > bestScore {
			bestScore = score
			bestPersonID = personID
		}
	}

	if bestScore >= attachThreshold {
		return bestPersonID, bestScore, true
	}
	return 0, bestScore, false
}

func (s *peopleService) markComponentPending(component []*model.Face, score float64) error {
	ids := faceIDs(component)
	if len(ids) == 0 {
		return nil
	}

	now := time.Now()
	if err := s.db.Model(&model.Face{}).Where("id IN ?", ids).Updates(map[string]interface{}{
		"person_id":      nil,
		"cluster_status": model.FaceClusterStatusPending,
		"cluster_score":  score,
		"clustered_at":   &now,
	}).Error; err != nil {
		return err
	}

	for _, face := range component {
		if face == nil {
			continue
		}
		face.PersonID = nil
		face.ClusterStatus = model.FaceClusterStatusPending
		face.ClusterScore = score
		face.ClusteredAt = &now
	}

	return nil
}

func (s *peopleService) createPersonFromComponent(component []*model.Face, score float64) (*model.Person, error) {
	ids := faceIDs(component)
	if len(ids) == 0 {
		return nil, fmt.Errorf("component is empty")
	}

	now := time.Now()
	person := &model.Person{Category: model.PersonCategoryStranger}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(person).Error; err != nil {
			return err
		}
		return tx.Model(&model.Face{}).Where("id IN ?", ids).Updates(map[string]interface{}{
			"person_id":      person.ID,
			"cluster_status": model.FaceClusterStatusAssigned,
			"cluster_score":  score,
			"clustered_at":   &now,
		}).Error
	}); err != nil {
		return nil, err
	}

	personID := person.ID
	for _, face := range component {
		if face == nil {
			continue
		}
		face.PersonID = &personID
		face.ClusterStatus = model.FaceClusterStatusAssigned
		face.ClusterScore = score
		face.ClusteredAt = &now
	}

	if err := s.syncPersonState(person.ID); err != nil {
		return nil, err
	}
	return s.personRepo.GetByID(person.ID)
}

func (s *peopleService) runIncrementalClustering() ([]uint, []uint, error) {
	pendingFaces, err := s.faceRepo.ListPending(0)
	if err != nil {
		return nil, nil, err
	}
	if len(pendingFaces) == 0 {
		return nil, nil, nil
	}

	assignedFaces, err := s.faceRepo.ListAssigned()
	if err != nil {
		return nil, nil, err
	}
	prototypes := s.selectPersonPrototypes(assignedFaces, peoplePrototypeCount)

	graph := s.buildFaceGraph(pendingFaces, peopleLinkThreshold)
	components := s.findConnectedComponents(graph)
	pendingByID := make(map[uint]*model.Face, len(pendingFaces))
	for _, face := range pendingFaces {
		if face == nil || face.ID == 0 {
			continue
		}
		pendingByID[face.ID] = face
	}

	affectedPersonIDs := make(map[uint]struct{})
	affectedPhotoIDs := make(map[uint]struct{})

	for _, componentIDs := range components {
		component := make([]*model.Face, 0, len(componentIDs))
		for _, faceID := range componentIDs {
			face, ok := pendingByID[faceID]
			if !ok {
				continue
			}
			component = append(component, face)
		}
		if len(component) == 0 {
			continue
		}

		personID, score, attached := s.attachComponentToExistingPerson(component, prototypes, peopleAttachThreshold)
		componentScore := nonNegativeScore(score)

		if attached {
			now := time.Now()
			if err := s.faceRepo.UpdateClusterFields(faceIDs(component), map[string]interface{}{
				"person_id":      personID,
				"cluster_status": model.FaceClusterStatusAssigned,
				"cluster_score":  componentScore,
				"clustered_at":   &now,
			}); err != nil {
				return nil, nil, err
			}
			for _, face := range component {
				if face == nil {
					continue
				}
				face.PersonID = &personID
				face.ClusterStatus = model.FaceClusterStatusAssigned
				face.ClusterScore = componentScore
				face.ClusteredAt = &now
			}
			affectedPersonIDs[personID] = struct{}{}
			for _, photoID := range facePhotoIDs(component) {
				affectedPhotoIDs[photoID] = struct{}{}
			}
			continue
		}

		if len(component) >= peopleMinClusterFaces {
			person, err := s.createPersonFromComponent(component, componentScore)
			if err != nil {
				return nil, nil, err
			}
			if person != nil && person.ID != 0 {
				affectedPersonIDs[person.ID] = struct{}{}
			}
			for _, photoID := range facePhotoIDs(component) {
				affectedPhotoIDs[photoID] = struct{}{}
			}
			continue
		}

		if err := s.markComponentPending(component, componentScore); err != nil {
			return nil, nil, err
		}
		for _, photoID := range facePhotoIDs(component) {
			affectedPhotoIDs[photoID] = struct{}{}
		}
	}

	return mapKeys(affectedPersonIDs), mapKeys(affectedPhotoIDs), nil
}

func (s *peopleService) syncPersonState(personID uint) error {
	person, err := s.personRepo.GetByID(personID)
	if err != nil {
		return err
	}
	if person == nil {
		return nil
	}

	faces, err := s.faceRepo.ListByPersonID(personID)
	if err != nil {
		return err
	}
	if len(faces) == 0 {
		return s.personRepo.Delete(personID)
	}

	if err := s.personRepo.RefreshStats(personID); err != nil {
		return err
	}

	if person.AvatarLocked && person.RepresentativeFaceID != nil {
		for _, face := range faces {
			if face.ID == *person.RepresentativeFaceID {
				return nil
			}
		}
		person.AvatarLocked = false
	}

	bestFace := faces[0]
	for _, face := range faces[1:] {
		if face.QualityScore > bestFace.QualityScore {
			bestFace = face
			continue
		}
		if face.QualityScore == bestFace.QualityScore && face.Confidence > bestFace.Confidence {
			bestFace = face
		}
	}

	updates := map[string]interface{}{
		"representative_face_id": bestFace.ID,
	}
	if !person.AvatarLocked {
		updates["avatar_locked"] = false
	}
	return s.personRepo.UpdateFields(personID, updates)
}

func facePhotoIDs(faces []*model.Face) []uint {
	seen := make(map[uint]struct{})
	photoIDs := make([]uint, 0, len(faces))
	for _, face := range faces {
		if face == nil || face.PhotoID == 0 {
			continue
		}
		if _, ok := seen[face.PhotoID]; ok {
			continue
		}
		seen[face.PhotoID] = struct{}{}
		photoIDs = append(photoIDs, face.PhotoID)
	}
	return photoIDs
}

func componentPhotoCount(component []*model.Face) int {
	return len(facePhotoIDs(component))
}

func faceIDs(faces []*model.Face) []uint {
	seen := make(map[uint]struct{})
	ids := make([]uint, 0, len(faces))
	for _, face := range faces {
		if face == nil || face.ID == 0 {
			continue
		}
		if _, ok := seen[face.ID]; ok {
			continue
		}
		seen[face.ID] = struct{}{}
		ids = append(ids, face.ID)
	}
	return ids
}

func mapKeys(values map[uint]struct{}) []uint {
	keys := make([]uint, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func nonNegativeScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	return score
}

func hasManualLockedFaces(faces []*model.Face) bool {
	for _, face := range faces {
		if face != nil && face.ManualLocked {
			return true
		}
	}
	return false
}

func filterFacesByOtherPhotos(faces []*model.Face, photoID uint) []*model.Face {
	filtered := make([]*model.Face, 0, len(faces))
	for _, face := range faces {
		if face == nil || face.PhotoID == photoID {
			continue
		}
		filtered = append(filtered, face)
	}
	return filtered
}

func personIDsFromFaces(faces []*model.Face) []uint {
	seen := make(map[uint]struct{})
	personIDs := make([]uint, 0, len(faces))
	for _, face := range faces {
		if face == nil || face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		personID := *face.PersonID
		if _, ok := seen[personID]; ok {
			continue
		}
		seen[personID] = struct{}{}
		personIDs = append(personIDs, personID)
	}
	return personIDs
}

func decodeEmbedding(payload []byte) []float32 {
	if len(payload) == 0 {
		return nil
	}
	var embedding []float32
	if err := json.Unmarshal(payload, &embedding); err != nil {
		return nil
	}
	return embedding
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return -1
	}

	var dot float64
	var normA float64
	var normB float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		normA += af * af
		normB += bf * bf
	}
	if normA == 0 || normB == 0 {
		return -1
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
