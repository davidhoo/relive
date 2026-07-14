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
}

// NewPersonPhotosBackfill 构造回填服务。db 应为后台只读连接（回填用 WriteQueue 写入）。
func NewPersonPhotosBackfill(db *gorm.DB, ppRepo repository.PersonPhotoRepository, coordinator *BackgroundTaskCoordinator) *PersonPhotosBackfill {
	return &PersonPhotosBackfill{
		db:           db,
		ppRepo:       ppRepo,
		coordinator:  coordinator,
		batchSize:    500,
		pauseBetween: 50 * time.Millisecond,
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
			time.Sleep(10 * time.Second)
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
		time.Sleep(10 * time.Second)
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
		// 无更多 face：跑一致性校验，通过则标记 ready。
		if err := b.ppRepo.RunVerification(b.db); err != nil {
			logger.Warnf("person_photos consistency check failed, will retry: %v", err)
			// 校验失败：可能是回填期间并发写入临时不一致，退避后重试。
			time.Sleep(10 * time.Second)
			return false, nil
		}
		if err := b.ppRepo.SetMigrationStatus(b.db, "ready", newLast); err != nil {
			return false, err
		}
		logger.Infof("person_photos backfill complete, status=ready")
		return true, nil
	}

	// 保存进度，服务重启可继续。
	if err := b.ppRepo.SetMigrationStatus(b.db, "backfilling", newLast); err != nil {
		return false, err
	}

	// 批次间让步，避免持续占用磁盘。
	time.Sleep(b.pauseBetween)
	return false, nil
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
