package service

import (
	"fmt"
	"math"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/google/uuid"
)

// IdentityComponentMatcher 是 primary 聚类决策所需的最小引擎接口。
type IdentityComponentMatcher interface {
	MatchComponent(component []*model.Face, opts IdentityRecallOptions) IdentityMatchResult
}

// identityPrimaryEnabled 报告当前是否启用 primary 接管。仅 primary 模式且注入了
// 统一引擎时为 true。禁止用 mode != legacy 作为 primary 判断。
func (s *peopleService) identityPrimaryEnabled() bool {
	return s.identityProfileMode == model.PeopleIdentityModePrimary && s.identityMatchingEngine != nil
}

// SetIdentityMatchingEngine 注入统一身份匹配引擎（primary 模式决策用）。
// 生产由 service.go 在非 legacy 模式装配；测试可注入 fake。
func (s *peopleService) SetIdentityMatchingEngine(engine IdentityComponentMatcher) {
	s.identityMatchingEngine = engine
}

// SetIdentityAutoStrategy 注入自动归属策略（primary 模式）。
func (s *peopleService) SetIdentityAutoStrategy(strategy IdentityStrategy) {
	s.identityAutoStrategy = strategy
}

// primaryComponentDecision 是 primary 模式下单个组件的决策结果。
type primaryComponentDecision struct {
	action     string // attach / create / pending / wait / reject / fail
	personID   uint
	score      float64
	margin     *float64
	reason     string
	engineRes  IdentityMatchResult
	decision   IdentityStrategyDecision
	createdNew bool
}

const (
	primaryActionAttach  = "attach"
	primaryActionCreate  = "create"
	primaryActionPending = "pending"
	primaryActionWait    = "wait"
	primaryActionReject  = "reject"
	primaryActionFail    = "fail"
)

// decidePrimaryComponent 对一个组件调用统一引擎一次并应用自动策略。
// 不调用 legacy 算法；不在 Available=false 时隐式回退。
func (s *peopleService) decidePrimaryComponent(component []*model.Face) primaryComponentDecision {
	engine := s.identityMatchingEngine
	strategy := s.identityAutoStrategy
	if strategy.Name == "" {
		strategy = NewIdentityAutoStrategy(s.config.People)
	}

	res := engine.MatchComponent(component, DefaultIdentityRecallOptions())
	// 非法分数视为无效输入
	if res.Best != nil && (math.IsNaN(res.Best.Score) || math.IsInf(res.Best.Score, 0)) {
		res.Status = IdentityMatchStatusInvalid
		res.BlockReason = blockInvalidQuery
	}

	out := primaryComponentDecision{
		engineRes: res,
	}
	if res.MarginApplicable {
		m := res.Margin
		out.margin = &m
	}

	// 部分召回证据不可读：与旧 matcher Available=false 对齐，fail-closed 等待，禁止带病自动吸附。
	if res.IncompleteEvidence {
		out.action = primaryActionWait
		out.reason = blockProfileUnavailable
		if res.BlockReason != "" {
			out.reason = res.BlockReason
		}
		// 仍记录已算出的 Best 分数，避免 wait 落库时 cluster_score 被抹成 0。
		if res.Best != nil {
			out.score = res.Best.Score
			out.personID = res.Best.PersonID
		}
		return out
	}

	dec := ApplyIdentityStrategy(res, strategy)
	out.decision = dec
	out.score = dec.Score
	out.reason = dec.Reason

	switch res.Status {
	case IdentityMatchStatusUnavailable:
		out.action = primaryActionWait
		out.reason = res.BlockReason
		if out.reason == "" {
			out.reason = identityRejectUnavailable
		}
		return out
	case IdentityMatchStatusInvalid:
		out.action = primaryActionFail
		out.reason = res.BlockReason
		if out.reason == "" {
			out.reason = identityRejectInvalid
		}
		return out
	case IdentityMatchStatusHardConflict:
		out.action = primaryActionReject
		out.reason = res.BlockReason
		if out.reason == "" {
			out.reason = identityRejectHardConflict
		}
		return out
	case IdentityMatchStatusNoCandidate:
		// 沿用现有创建新人物规则
		out.action = primaryActionCreate
		return out
	}

	if dec.Accepted {
		out.action = primaryActionAttach
		out.personID = dec.PersonID
		out.score = dec.Score
		return out
	}

	// 有候选但未达自动策略：待定，不通过创建新人绕过不确定结果
	if res.Best != nil && res.Best.PersonID != 0 {
		out.action = primaryActionPending
		out.personID = res.Best.PersonID
		out.score = res.Best.Score
		if out.reason == "" {
			out.reason = res.BlockReason
		}
		return out
	}

	out.action = primaryActionPending
	return out
}

// applyPrimaryComponentDecision 执行 primary 决策写入。
func (s *peopleService) applyPrimaryComponentDecision(
	component []*model.Face,
	decision primaryComponentDecision,
	affectedPersonIDs map[uint]struct{},
	affectedPhotoIDs map[uint]struct{},
) error {
	before := cloneComponentForShadow(component)
	switch decision.action {
	case primaryActionAttach:
		prevIDs, err := s.attachComponentToPerson(component, decision.personID, nonNegativeScore(decision.score))
		if err != nil {
			return err
		}
		affectedPersonIDs[decision.personID] = struct{}{}
		for _, pid := range prevIDs {
			affectedPersonIDs[pid] = struct{}{}
		}
		for _, photoID := range facePhotoIDs(component) {
			affectedPhotoIDs[photoID] = struct{}{}
		}
		s.recordPrimaryAssignmentChanges(component, before, decision, model.PeopleIdentityDecisionSourceProfileAttach, false)
		return nil

	case primaryActionCreate:
		maxRetry := 0
		for _, face := range component {
			if face != nil && face.RetryCount > maxRetry {
				maxRetry = face.RetryCount
			}
		}
		canCreate := len(component) >= peopleMinClusterFaces && componentPhotoCount(component) >= 2
		if !canCreate && maxRetry >= singleFaceFallbackRetries {
			canCreate = true
		}
		if canCreate {
			person, err := s.createPersonFromComponent(component, nonNegativeScore(decision.score))
			if err != nil {
				return err
			}
			if person != nil && person.ID != 0 {
				affectedPersonIDs[person.ID] = struct{}{}
			}
			for _, photoID := range facePhotoIDs(component) {
				affectedPhotoIDs[photoID] = struct{}{}
			}
			s.recordPrimaryAssignmentChanges(component, before, decision, model.PeopleIdentityDecisionSourceCreatePerson, true)
			return nil
		}
		return s.markComponentPendingWithReason(component, nonNegativeScore(decision.score), decision.reason, false)

	case primaryActionPending, primaryActionReject:
		return s.markComponentPendingWithReason(component, nonNegativeScore(decision.score), decision.reason, false)

	case primaryActionWait:
		return s.markComponentPendingWithReason(component, nonNegativeScore(decision.score), decision.reason, true)

	case primaryActionFail:
		return s.markComponentIdentityFailed(component, decision.reason)

	default:
		return fmt.Errorf("unknown primary action %q", decision.action)
	}
}

// markComponentPendingWithReason 写入待定，可选技术故障退避（不增加语义 RetryCount）。
func (s *peopleService) markComponentPendingWithReason(component []*model.Face, score float64, reason string, technicalWait bool) error {
	now := time.Now()
	ids := make([]uint, 0, len(component))
	for _, face := range component {
		if face == nil || face.ID == 0 {
			continue
		}
		ids = append(ids, face.ID)
	}
	if len(ids) == 0 {
		return nil
	}

	return s.executeWrite(func() error {
		for _, face := range component {
			if face == nil || face.ID == 0 {
				continue
			}
			fields := map[string]interface{}{
				"cluster_status":          model.FaceClusterStatusPending,
				"cluster_score":           score,
				"clustered_at":            now,
				"identity_failure_reason": reason,
			}
			if technicalWait {
				count := face.IdentityUnavailableCount + 1
				fields["identity_unavailable_count"] = count
				fields["identity_retry_after"] = identityUnavailableBackoff(now, count)
				// 技术故障不增加语义 RetryCount
			} else {
				fields["retry_count"] = face.RetryCount + 1
				fields["identity_unavailable_count"] = 0
				fields["identity_retry_after"] = nil
			}
			if err := s.faceRepo.UpdateFields(face.ID, fields); err != nil {
				return err
			}
		}
		return nil
	})
}

// markComponentIdentityFailed 将组件标记为可诊断失败状态（仍用 pending + 原因，避免扩大状态枚举破坏兼容）。
func (s *peopleService) markComponentIdentityFailed(component []*model.Face, reason string) error {
	now := time.Now()
	return s.executeWrite(func() error {
		for _, face := range component {
			if face == nil || face.ID == 0 {
				continue
			}
			fields := map[string]interface{}{
				"cluster_status":             model.FaceClusterStatusPending,
				"clustered_at":               now,
				"identity_failure_reason":    reason,
				"identity_retry_after":       now.Add(24 * time.Hour), // 需人工/修复后才再试
				"identity_unavailable_count": 0,
			}
			if err := s.faceRepo.UpdateFields(face.ID, fields); err != nil {
				return err
			}
		}
		return nil
	})
}

// identityUnavailableBackoff 技术不可用指数退避：30s * 2^(n-1)，上限 15m。
func identityUnavailableBackoff(now time.Time, count int) time.Time {
	if count < 1 {
		count = 1
	}
	secs := 30 * (1 << (count - 1))
	if secs > 15*60 {
		secs = 15 * 60
	}
	return now.Add(time.Duration(secs) * time.Second)
}

// recordPrimaryShadowObservation 在 primary 写入后记录遥测（可选），标明是否计算了 legacy。
func (s *peopleService) recordPrimaryObservation(component []*model.Face, decision primaryComponentDecision, legacyComputed bool) *identityShadowObservation {
	if !s.identityShadowEnabled() {
		return nil
	}
	obs := &identityShadowObservation{
		Component: cloneComponentForShadow(component),
		Legacy: legacyIdentityResult{
			Matched: false, // primary 不计算 legacy；不得记为 miss
		},
		ProfileComputed: true,
		Profile: IdentityProfileMatch{
			Available:    decision.engineRes.Status != IdentityMatchStatusUnavailable && decision.engineRes.Status != IdentityMatchStatusInvalid,
			PersonID:     decision.personID,
			Score:        decision.score,
			AutoEligible: decision.action == primaryActionAttach,
			BlockReason:  decision.reason,
		},
	}
	if decision.engineRes.Best != nil {
		obs.Profile.PersonID = decision.engineRes.Best.PersonID
		obs.Profile.Score = decision.engineRes.Best.Score
		obs.Profile.CenterIDs = append([]uint{}, decision.engineRes.Best.CenterIDs...)
	}
	if decision.engineRes.Second != nil {
		obs.Profile.SecondPersonID = decision.engineRes.Second.PersonID
		obs.Profile.SecondScore = decision.engineRes.Second.Score
	}
	if decision.engineRes.MarginApplicable {
		obs.Profile.Margin = decision.engineRes.Margin
	}
	if decision.action == primaryActionAttach {
		obs.RescueApplied = true // 复用遥测分类：画像实际写入
	}
	_ = legacyComputed
	return obs
}

// logPrimaryDecision 输出脱敏诊断日志。
func logPrimaryDecision(action, reason string, personID uint, score float64) {
	logger.Infof("people clustering primary: action=%s reason=%s target=%d score=%.4f", action, reason, personID, score)
}

// SetIdentityAssignmentRepo 注入归属批次仓库，用于 primary 写入完整变更日志。
func (s *peopleService) SetIdentityAssignmentRepo(repo repository.PeopleIdentityAssignmentRepository) {
	s.identityAssignmentRepo = repo
}

// beginIdentityAssignmentBatch 在 coordinator 批次开始时创建 running 批次。
func (s *peopleService) beginIdentityAssignmentBatch(source string) {
	s.currentAssignmentBatchID = 0
	if !s.identityPrimaryEnabled() || s.identityAssignmentRepo == nil {
		return
	}
	strategy := s.identityAutoStrategy
	fp := IdentityStrategyFingerprint(s.config.People)
	engineVer := identityEngineVersion
	if s.identityMatchingEngine != nil {
		if e, ok := s.identityMatchingEngine.(*IdentityMatchingEngine); ok {
			engineVer = e.EngineVersion()
		}
	}
	batch := &model.PeopleIdentityAssignmentBatch{
		OperationID:       uuid.NewString(),
		Source:            source,
		Mode:              model.PeopleIdentityModePrimary,
		EngineVersion:     engineVer,
		StrategyVersion:   strategy.Version,
		ConfigFingerprint: fp,
		Status:            model.PeopleIdentityAssignmentBatchRunning,
		StartedAt:         time.Now(),
	}
	if err := s.identityAssignmentRepo.CreateBatch(batch); err != nil {
		logger.Warnf("identity assignment: create batch failed: %v", err)
		return
	}
	s.currentAssignmentBatchID = batch.ID
}

// finalizeIdentityAssignmentBatch 结束当前批次状态。
func (s *peopleService) finalizeIdentityAssignmentBatch(status string, assignedComponents, assignedFaces, pendingComponents int) {
	if s.currentAssignmentBatchID == 0 || s.identityAssignmentRepo == nil {
		return
	}
	batch, err := s.identityAssignmentRepo.GetBatch(s.currentAssignmentBatchID)
	if err != nil {
		return
	}
	now := time.Now()
	batch.Status = status
	batch.AssignedComponents = assignedComponents
	batch.AssignedFaces = assignedFaces
	batch.PendingComponents = pendingComponents
	batch.CompletedAt = &now
	_ = s.identityAssignmentRepo.UpdateBatch(batch)
	s.currentAssignmentBatchID = 0
}

// recordPrimaryAssignmentChanges 在一次成功的 attach/create 后写入完整人脸变更日志。
func (s *peopleService) recordPrimaryAssignmentChanges(
	component []*model.Face,
	before []*model.Face,
	decision primaryComponentDecision,
	decisionSource string,
	newPersonCreated bool,
) {
	if s.currentAssignmentBatchID == 0 || s.identityAssignmentRepo == nil {
		return
	}
	componentKey := fmt.Sprintf("c-%d", component[0].ID)
	if len(component) > 1 {
		componentKey = fmt.Sprintf("c-%d-%d", component[0].ID, component[len(component)-1].ID)
	}
	beforeByID := make(map[uint]*model.Face, len(before))
	for _, f := range before {
		if f != nil {
			beforeByID[f.ID] = f
		}
	}
	changes := make([]model.PeopleIdentityAssignmentChange, 0, len(component))
	for _, face := range component {
		if face == nil {
			continue
		}
		old := beforeByID[face.ID]
		ch := model.PeopleIdentityAssignmentChange{
			BatchID:          s.currentAssignmentBatchID,
			ComponentKey:     componentKey,
			FaceID:           face.ID,
			DecisionSource:   decisionSource,
			NewPersonID:      face.PersonID,
			NewClusterStatus: face.ClusterStatus,
			NewClusterScore:  face.ClusterScore,
			NewClusteredAt:   face.ClusteredAt,
			NewRetryCount:    face.RetryCount,
			Score:            decision.score,
			Margin:           decision.margin,
			Reason:           decision.reason,
			CenterIDsJSON:    "[]",
			NewPersonCreated: newPersonCreated,
		}
		if old != nil {
			ch.OldPersonID = old.PersonID
			ch.OldClusterStatus = old.ClusterStatus
			ch.OldClusterScore = old.ClusterScore
			ch.OldClusteredAt = old.ClusteredAt
			ch.OldRetryCount = old.RetryCount
			ch.PreviousAssignmentVersion = old.AssignmentVersion
		}
		// 读取提交后版本
		if latest, err := s.faceRepo.GetByID(face.ID); err == nil && latest != nil {
			ch.CommittedAssignmentVersion = latest.AssignmentVersion
			ch.NewPersonID = latest.PersonID
			ch.NewClusterStatus = latest.ClusterStatus
			ch.NewClusterScore = latest.ClusterScore
			ch.NewClusteredAt = latest.ClusteredAt
		}
		changes = append(changes, ch)
	}
	if err := s.identityAssignmentRepo.CreateChanges(nil, changes); err != nil {
		logger.Warnf("identity assignment: create changes failed: %v", err)
	}
}
