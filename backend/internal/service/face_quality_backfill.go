package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
)

// FaceQualityBackfill 在后台低并对存量 Face 生成质检候选与审计事件。
//
// 设计要点（对齐任务说明 §7 存量审计）：
//   - 只写审计事件与证据快照，不改人物归属（不聚类、不分配 person_id）；
//   - 进度记在 app_config（key=migration.face_quality_backfill_v1），服务重启可继续；
//   - foreground active / I/O 高时暂停，避免与前台和 NAS 磁盘争抢；
//   - 可暂停（pauseMu）、可继续（Run 自动从进度恢复）、可按规则版本恢复（RestoreAuto）。
//
// 与 ApplyDetectionResult 的实时质检共享 evaluateFaceQuality 策略引擎，但走只读路径：
// 实时质检会写 Face 排除态并影响聚类；存量审计只产候选（shadow 语义），
// 是否自动排除由 face_quality_mode 决定——enforce 时同样只产候选，不回改历史 Face，
// 避免存量批处理触发大规模重聚类（任务说明明确“存量数据不得直接全库重聚类”）。
type FaceQualityBackfill struct {
	people       *peopleService
	coordinator  *BackgroundTaskCoordinator
	progressRepo repository.ConfigRepository // 用 AppConfig 进度持久化

	batchSize    int
	pauseBetween time.Duration
	backoff      time.Duration

	pauseMu  sync.RWMutex
	paused   atomic.Bool
	progress atomic.Uint64 // 最近处理的 face_id，内存镜像，启动时从 DB 加载
}

const (
	faceQualityBackfillProgressKey = "migration.face_quality_backfill_v1"
	faceQualityBackfillBatchSize   = 200
)

// NewFaceQualityBackfill 构造存量质检审计后台任务。
func NewFaceQualityBackfill(people *peopleService, coordinator *BackgroundTaskCoordinator, progressRepo repository.ConfigRepository) *FaceQualityBackfill {
	return &FaceQualityBackfill{
		people:       people,
		coordinator:  coordinator,
		progressRepo: progressRepo,
		batchSize:    faceQualityBackfillBatchSize,
		pauseBetween: 100 * time.Millisecond,
		backoff:      10 * time.Second,
	}
}

// Run 启动后台审计 goroutine（非阻塞）。服务重启后从 app_config 进度继续。
func (b *FaceQualityBackfill) Run() {
	b.loadProgress()
	go b.loop()
}

// Pause 暂停存量审计（可继续）。
func (b *FaceQualityBackfill) Pause() {
	b.paused.Store(true)
	logger.Infof("face_quality backfill paused")
}

// Resume 恢复存量审计。
func (b *FaceQualityBackfill) Resume() {
	b.paused.Store(false)
	logger.Infof("face_quality backfill resumed")
}

// IsPaused 返回暂停状态。
func (b *FaceQualityBackfill) IsPaused() bool { return b.paused.Load() }

// Progress 返回最近处理的 face_id。
func (b *FaceQualityBackfill) Progress() uint64 { return b.progress.Load() }

func (b *FaceQualityBackfill) loadProgress() {
	if b.progressRepo == nil {
		return
	}
	cfg, err := b.progressRepo.Get(faceQualityBackfillProgressKey)
	if err == nil && cfg != nil {
		var v uint64
		fmt.Sscanf(cfg.Value, "%d", &v)
		b.progress.Store(v)
	}
}

func (b *FaceQualityBackfill) saveProgress(faceID uint64) {
	b.progress.Store(faceID)
	if b.progressRepo == nil {
		return
	}
	_ = b.progressRepo.Set(faceQualityBackfillProgressKey, fmt.Sprintf("%d", faceID))
}

// loop 单线程按批次扫描，直到无更多未处理 Face 或被持续暂停。
func (b *FaceQualityBackfill) loop() {
	for {
		if b.paused.Load() {
			time.Sleep(b.backoff)
			continue
		}
		done, err := b.runOnce()
		if err != nil {
			logger.Warnf("face_quality backfill error: %v", err)
			time.Sleep(b.backoff)
			continue
		}
		if done {
			// 扫描到末尾：退避后继续轮询，因为新照片可能持续产生未处理 Face。
			time.Sleep(b.backoff)
		}
	}
}

// runOnce 执行一个批次。done=true 表示本轮无更多未处理 Face。
func (b *FaceQualityBackfill) runOnce() (bool, error) {
	// 暂停态：不处理新批次，返回未完成。loop 会按 backoff 重新检查。
	if b.paused.Load() {
		return false, nil
	}
	// foreground active 时暂停。
	if b.coordinator != nil && b.coordinator.ForegroundActive() {
		time.Sleep(2 * time.Second)
		return false, nil
	}
	// I/O 压力高时暂停（advisory）。
	if b.coordinator != nil {
		snap := b.coordinator.LoadSnapshot()
		if b.coordinator.IOWaitPauseThreshold() > 0 && snap.CPUIOWaitPct >= b.coordinator.IOWaitPauseThreshold() {
			time.Sleep(2 * time.Second)
			return false, nil
		}
	}

	afterID := uint(b.progress.Load())
	faces, err := b.people.faceRepo.ListUnprocessedForQuality(afterID, b.batchSize)
	if err != nil {
		// face_quality_events 表可能尚未建好（迁移进行中），退避重试。
		return false, fmt.Errorf("list unprocessed faces: %w", err)
	}
	if len(faces) == 0 {
		return true, nil
	}

	for _, face := range faces {
		// 存量审计：只产候选审计事件，不改 Face 排除态、不分配 person_id。
		// 用 Face 上已有的证据快照（ApplyDetectionResult 写入）回放判定；
		// 无证据快照的旧 Face 标记 review_required，交人工审核（fail-closed）。
		decision, reason, reasonCodes := b.replayDecision(face)

		// 跳过已存在当前事件的（并发安全：NOT EXISTS 已过滤，此处再保险一次）。
		existing, _ := b.people.faceQualityRepo.ListCurrentByPhotoID(face.PhotoID)
		if hasCurrentEventForBBox(existing, face) {
			b.saveProgress(uint64(face.ID))
			continue
		}

		evt := &model.FaceQualityEvent{
			PhotoID:      face.PhotoID,
			FaceID:       &face.ID,
			BBoxX:        face.BBoxX,
			BBoxY:        face.BBoxY,
			BBoxWidth:    face.BBoxWidth,
			BBoxHeight:   face.BBoxHeight,
			Decision:     decision,
			Reason:       reason,
			Source:       model.FaceQualitySourceAuto,
			RuleVersion:  nonEmpty(face.QualityRuleVersion, "v1"),
			ModelVersion: face.QualityModelVersion,
			ReasonCodes:  reasonCodesCSV(reasonCodes),
			IsCurrent:    true,
		}
		// 存量审计事件一律标 historical_backfill + legacy_v1（v1 同源证据快照回放）。
		evt.EvidenceOrigin = model.FaceQualityEvidenceOriginHistoricalBackfill
		evt.EvidencePipeline = model.FaceQualityEvidencePipelineLegacyV1
		// 若 Face 上有证据快照，尝试构造证据 JSON。
		if face.FaceValidityScore > 0 || face.QualityReasonsCSV != "" {
			ev := &model.FaceQualityEvidence{
				FaceValidityScore: face.FaceValidityScore,
				PixelWidth:        0, // 存量回放无像素信息
				QualityReasons:    splitReasonCodes(face.QualityReasonsCSV),
				RuleVersion:       nonEmpty(face.QualityRuleVersion, "v1"),
				ModelVersion:      face.QualityModelVersion,
			}
			if b, err := json.Marshal(ev); err == nil {
				evt.EvidenceJSON = string(b)
			}
		}
		// 证据状态：有可解析 evidence → available；否则 missing（不混入人工审核）。
		if strings.TrimSpace(evt.EvidenceJSON) != "" {
			evt.EvidenceState = model.FaceQualityEvidenceStateAvailable
		} else {
			evt.EvidenceState = model.FaceQualityEvidenceStateMissing
		}
		if err := b.people.faceQualityRepo.Create(evt); err != nil {
			logger.Warnf("face_quality backfill create event for face %d: %v", face.ID, err)
			// 单条失败不终止整批，推进进度避免卡死。
		}
		b.saveProgress(uint64(face.ID))
	}

	time.Sleep(b.pauseBetween)
	return false, nil
}

// replayDecision 用 Face 证据快照回放策略引擎判定。
// 无证据快照的旧 Face → review_required（fail-closed，交人工）。
func (b *FaceQualityBackfill) replayDecision(face *model.Face) (decision, reason string, reasonCodes []string) {
	if face.QualityRuleVersion == "" && face.FaceValidityScore == 0 {
		// 无证据快照：灰区，交人工审核。
		return model.FaceQualityDecisionReviewRequired, "", nil
	}
	ev := &model.FaceQualityEvidence{
		FaceValidityScore:     face.FaceValidityScore,
		LandmarkCompleteness:  1.0,
		LandmarkGeometryScore: 1.0,
		QualityReasons:        splitReasonCodes(face.QualityReasonsCSV),
		RuleVersion:           nonEmpty(face.QualityRuleVersion, "v1"),
		ModelVersion:          face.QualityModelVersion,
	}
	// 存量审计用 shadow 语义回放：只产候选，不自动排除（不回改历史 Face）。
	out := evaluateFaceQuality(ev, "shadow")
	return out.Decision, out.Reason, out.ReasonCodes
}

// hasCurrentEventForBBox 检查某照片是否已有匹配该 Face bbox 的当前质检事件。
func hasCurrentEventForBBox(events []*model.FaceQualityEvent, face *model.Face) bool {
	for _, e := range events {
		if e == nil || !e.IsCurrent {
			continue
		}
		if bboxIoU(e.BBoxX, e.BBoxY, e.BBoxWidth, e.BBoxHeight, face.BBoxX, face.BBoxY, face.BBoxWidth, face.BBoxHeight) > exclusionIoUThreshold {
			return true
		}
	}
	return false
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
