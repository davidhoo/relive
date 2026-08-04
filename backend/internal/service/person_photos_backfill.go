package service

import (
	"time"

	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// PersonPhotosBackfill 在后台异步回填 person_photos 派生表的历史数据。
//
// 启动时 migratePersonPhotosTable 已创建表/索引/trigger（trigger 在回填前安装，接住回填期间
// 新变更）。本服务按批次扫描 faces，进度记在 app_config，foreground active / I/O 高时暂停，
// 服务重启后从进度继续。一致性校验通过后才把 status 标记 ready，handler 才切换到索引查询。
//
// 回填未完成时人物照片接口继续使用原 JOIN 查询，不影响首屏可用性。
type PersonPhotosBackfill struct {
	db           *gorm.DB
	ppRepo       repository.PersonPhotoRepository
	coordinator  *BackgroundTaskCoordinator
	batchSize    int
	pauseBetween time.Duration
	// backoff 是异常/未就绪/修复失败/仍不一致等退避路径的等待时间。
	// 生产默认 10 秒；测试可在同包内覆盖为极短值。<=0 时回退默认，避免 tight loop。
	backoff time.Duration
	// stalledBackoff 是零进度退避：修复一轮无修改但仍有不一致时使用更长等待。
	// 生产默认 50 秒（5x backoff）；测试可覆盖为极短值。<=0 时回退 5x backoff。
	stalledBackoff time.Duration
}

// defaultBackoff 是生产环境的退避等待时间。
const defaultBackoff = 10 * time.Second

// stalledBackoffMultiplier 是零进度退避相对正常 backoff 的倍数。
const stalledBackoffMultiplier = 5

// effectiveBackoff 返回生效退避时间，backoff<=0 时回退默认值。
func (b *PersonPhotosBackfill) effectiveBackoff() time.Duration {
	if b.backoff <= 0 {
		return defaultBackoff
	}
	return b.backoff
}

// effectiveStalledBackoff 返回零进度退避时间。stalledBackoff<=0 时回退 5x effectiveBackoff。
func (b *PersonPhotosBackfill) effectiveStalledBackoff() time.Duration {
	if b.stalledBackoff <= 0 {
		return time.Duration(stalledBackoffMultiplier) * b.effectiveBackoff()
	}
	return b.stalledBackoff
}

// NewPersonPhotosBackfill 构造回填服务。db 应为后台只读连接（回填用 WriteQueue 写入）。
func NewPersonPhotosBackfill(db *gorm.DB, ppRepo repository.PersonPhotoRepository, coordinator *BackgroundTaskCoordinator) *PersonPhotosBackfill {
	return &PersonPhotosBackfill{
		db:             db,
		ppRepo:         ppRepo,
		coordinator:    coordinator,
		batchSize:      500,
		pauseBetween:   50 * time.Millisecond,
		backoff:        defaultBackoff,
		stalledBackoff: 0, // <=0 → effectiveStalledBackoff 回退 5x backoff
	}
}

// Run 启动后台回填 goroutine（非阻塞）。服务重启后从 app_config 进度继续。
func (b *PersonPhotosBackfill) Run() {
	go b.loop()
}

// loop 单线程按批次回填，直到没有更多 face 或出错。每批次后检查 foreground / 负载让步。
func (b *PersonPhotosBackfill) loop() {
	for {
		done, err := b.runOnce()
		if err != nil {
			logger.Warnf("person_photos backfill error: %v", err)
			// 出错后退避一段时间再重试，避免 tight loop。
			time.Sleep(b.effectiveBackoff())
			continue
		}
		if done {
			return
		}
	}
}

// runOnce 执行一个批次。done=true 表示回填完成（无更多 face）。
func (b *PersonPhotosBackfill) runOnce() (bool, error) {
	// foreground active 时暂停：不启动新批次。coordinator 为 nil 时放行（测试）。
	if b.coordinator != nil && b.coordinator.ForegroundActive() {
		time.Sleep(2 * time.Second)
		return false, nil
	}
	// I/O 压力高时暂停（advisory）。
	if b.coordinator != nil {
		snap := b.coordinator.LoadSnapshot()
		if isKnown(snap.CPUIOWaitPct) && b.coordinator.IOWaitPauseThreshold() > 0 && snap.CPUIOWaitPct >= b.coordinator.IOWaitPauseThreshold() {
			time.Sleep(2 * time.Second)
			return false, nil
		}
	}

	// 表尚未建好（启动迁移失败/进行中）时直接退避，避免对不存在的表写状态。
	hasTable, err := b.personPhotosTableExists()
	if err != nil {
		return false, err
	}
	if !hasTable {
		time.Sleep(b.effectiveBackoff())
		return false, nil
	}

	status, lastFaceID, err := b.ppRepo.GetMigrationStatus(b.db)
	if err != nil {
		return false, err
	}
	if status == "ready" {
		// 已完成，不再回填。
		return true, nil
	}

	// 首次：标记为 backfilling。
	if status == "" {
		if err := b.ppRepo.SetMigrationStatus(b.db, "backfilling", 0); err != nil {
			return false, err
		}
	}

	// 通过注入的 db 连接执行批次写入（与 trigger 同事务，trigger 自动接住）。
	// 不直接用全局 WriteQueue：测试场景 GetWriteDB() 可能为 nil，且回填属 P2 后台，
	// 经 coordinator 准入即可，无需抢占单连接写队列。
	newLast, inserted, err := b.ppRepo.BackfillBatch(b.db, lastFaceID, b.batchSize)
	if err != nil {
		return false, err
	}

	if inserted == 0 && newLast == lastFaceID {
		// 回填扫描到末尾。进入 v2 修复流程：先修复，再校验。
		// 关键修复点：旧版在此直接 RunVerification，校验失败后只 sleep 重试，永远不修复 →
		// 永远卡在 backfilling。新版改为：校验失败 → 跑修复批次 → 重新校验，全部清零才 ready。
		return b.finalizeAfterBackfill(newLast)
	}

	// 保存进度，服务重启可继续。
	if err := b.ppRepo.SetMigrationStatus(b.db, "backfilling", newLast); err != nil {
		return false, err
	}

	// 批次间让步，避免持续占用磁盘。
	time.Sleep(b.pauseBetween)
	return false, nil
}

// finalizeAfterBackfill 在回填扫描到末尾后执行“修复 → 校验 → ready”收尾。
//
// 流程：
//  1. 先跑一次校验，拿结构化报告。若已 clean → 直接 ready。
//  2. 不 clean → 标记 repairing，循环跑 RepairBatch 直到无剩余或达到本回合上限。
//  3. 修复后重新校验；全部为 0 → v1/v2 双标 ready。
//  4. 仍有剩余 → 退避后下回合继续（不重复全量校验空转：只在修复批次后才校验）。
//
// 这样保证：一致性失败后不再每 10s 空转同一全量校验，而是真正修复数据。
func (b *PersonPhotosBackfill) finalizeAfterBackfill(newLast uint) (bool, error) {
	rep, err := b.ppRepo.RunConsistencyCheck(b.db)
	if err != nil {
		return false, err
	}
	logger.Infof("person_photos consistency report: missing=%d extra=%d orphan=%d taken_at=%d person_count=%d orphan_faces=%d",
		rep.MissingAssociations, rep.ExtraAssociations, rep.OrphanPhotos, rep.TakenAtMismatches, rep.PersonCountMismatch, rep.OrphanFaces)

	if rep.IsClean() {
		if err := b.markReady(newLast); err != nil {
			return false, err
		}
		logger.Infof("person_photos backfill complete, status=ready (clean on first check)")
		return true, nil
	}

	// 不 clean：进入修复。标记 repairing 防止重启后误判为已完成。
	if err := b.ppRepo.SetMigrationStatusV2(b.db, "repairing", 0); err != nil {
		return false, err
	}

	// 本回合最多修复 maxRounds 轮（每轮受 batchSize 限制），避免单回合占用过久。
	const maxRounds = 20
	stalled := false
	for i := 0; i < maxRounds; i++ {
		delta, err := b.ppRepo.RepairBatch(b.db, b.batchSize)
		if err != nil {
			logger.Warnf("person_photos repair batch error: %v", err)
			time.Sleep(b.effectiveBackoff())
			return false, nil
		}
		logger.Infof("person_photos repair batch: deleted_extra=%d deleted_orphan=%d inserted_missing=%d fixed_taken_at=%d deleted_orphan_faces=%d (remaining: missing=%d extra=%d orphan=%d taken_at=%d orphan_faces=%d)",
			delta.ExtraDeleted, delta.OrphanDeleted, delta.MissingInserted, delta.TakenAtFixed, delta.OrphanFacesDeleted,
			delta.RemainingMissing, delta.RemainingExtra, delta.RemainingOrphan, delta.RemainingTakenAt, delta.RemainingOrphanFaces)

		// 让步，foreground/I/O 高时暂停（修复也属 P2）。
		if b.shouldYield() {
			time.Sleep(2 * time.Second)
		} else {
			time.Sleep(b.pauseBetween)
		}

		// 无任何修复动作且无剩余 → 校验通过判定。
		if delta.ExtraDeleted == 0 && delta.OrphanDeleted == 0 && delta.MissingInserted == 0 && delta.TakenAtFixed == 0 && delta.OrphanFacesDeleted == 0 &&
			delta.RemainingMissing == 0 && delta.RemainingExtra == 0 && delta.RemainingOrphan == 0 && delta.RemainingTakenAt == 0 && delta.RemainingOrphanFaces == 0 {
			break
		}

		// 零进度检测：本轮无任何修改但仍有不一致计数 → 不可修复数据，终止本轮避免无限重试。
		if delta.ExtraDeleted == 0 && delta.OrphanDeleted == 0 && delta.MissingInserted == 0 && delta.TakenAtFixed == 0 && delta.OrphanFacesDeleted == 0 &&
			(delta.RemainingMissing > 0 || delta.RemainingExtra > 0 || delta.RemainingOrphan > 0 || delta.RemainingTakenAt > 0 || delta.RemainingOrphanFaces > 0) {
			logger.Warnf("person_photos repair stalled: no changes this round but issues remain (missing=%d extra=%d orphan=%d taken_at=%d orphan_faces=%d) — backing off",
				delta.RemainingMissing, delta.RemainingExtra, delta.RemainingOrphan, delta.RemainingTakenAt, delta.RemainingOrphanFaces)
			stalled = true
			break
		}
	}

	// 修复后重新校验。
	// stalled 时跳过冗余全表校验（已知 dirty），直接走更长退避路径。
	if stalled {
		logger.Warnf("person_photos repair stalled: using longer backoff (%v), skipping redundant full check", b.effectiveStalledBackoff())
		time.Sleep(b.effectiveStalledBackoff())
		return false, nil
	}
	rep2, err := b.ppRepo.RunConsistencyCheck(b.db)
	if err != nil {
		return false, err
	}
	logger.Infof("person_photos post-repair report: missing=%d extra=%d orphan=%d taken_at=%d person_count=%d orphan_faces=%d",
		rep2.MissingAssociations, rep2.ExtraAssociations, rep2.OrphanPhotos, rep2.TakenAtMismatches, rep2.PersonCountMismatch, rep2.OrphanFaces)

	if rep2.IsClean() {
		if err := b.markReady(newLast); err != nil {
			return false, err
		}
		logger.Infof("person_photos backfill complete, status=ready (after repair)")
		return true, nil
	}

	// 仍有不一致：退避后下回合继续修复（不空转校验）。
	logger.Warnf("person_photos still inconsistent after repair, will continue next round")
	time.Sleep(b.effectiveBackoff())
	return false, nil
}

// markReady 同时标记 v1/v2 为 ready，并记录进度。handler 的 MigrationReady 检查任一 ready 即放行。
func (b *PersonPhotosBackfill) markReady(lastFaceID uint) error {
	if err := b.ppRepo.SetMigrationStatus(b.db, "ready", lastFaceID); err != nil {
		return err
	}
	return b.ppRepo.SetMigrationStatusV2(b.db, "ready", lastFaceID)
}

// shouldYield 报告修复批次间是否应让步（foreground active 或 I/O 高）。
func (b *PersonPhotosBackfill) shouldYield() bool {
	if b.coordinator == nil {
		return false
	}
	if b.coordinator.ForegroundActive() {
		return true
	}
	snap := b.coordinator.LoadSnapshot()
	if isKnown(snap.CPUIOWaitPct) && b.coordinator.IOWaitPauseThreshold() > 0 && snap.CPUIOWaitPct >= b.coordinator.IOWaitPauseThreshold() {
		return true
	}
	return false
}

// personPhotosTableExists 检查 person_photos 表是否已创建（避免在迁移失败时对不存在的表写状态）。
func (b *PersonPhotosBackfill) personPhotosTableExists() (bool, error) {
	var name string
	err := b.db.Raw(`SELECT name FROM sqlite_master WHERE type='table' AND name='person_photos' LIMIT 1`).Scan(&name).Error
	if err != nil {
		return false, err
	}
	return name == "person_photos", nil
}
