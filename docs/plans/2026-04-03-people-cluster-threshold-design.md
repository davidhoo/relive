# People Cluster Threshold Design

**Goal:** 轻微放松人物归属阈值，让相似但未达到当前极高阈值的人脸更容易并入已有人物。

**Scope:** `backend/internal/service/people_service.go`、`backend/internal/service/people_service_test.go`

## Problem

当前人物归属阈值写死为 `0.92`。这会使一些本应归到同一人物、但 embedding 相似度在 `0.88 ~ 0.91` 区间的人脸被拆成新人物，导致人物库碎片化。

## Decision

- 保持人脸检测阈值 `MinConfidence = 0.5` 不变
- 仅将人物归属阈值从 `0.92` 调整为 `0.88`
- 用一条边界测试覆盖“相似度约 `0.89` 应并入已有人物”

## Why This Approach

- `0.88` 属于轻微放松，不是激进下调
- 比 `0.90` 更容易观察到效果
- 又没有到 `0.85` 那样明显增加误并风险
- 先做常量调整，成本最低；若后续仍需调参，再考虑做成配置项

## Verification

- 新增测试：
  - 相似度 `0.89` 的样本应并入现有人物
- 运行相关测试：
  - `go test -run TestPeopleServiceCluster -v ./internal/service`
