# People Cluster Threshold Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Superseded
> **Note:** A later clustering redesign replaced this one-constant threshold change; do not treat this plan as current backlog.

**Goal:** 将人物归属阈值从 `0.92` 轻微放松到 `0.88`，减少同一人物被拆成多个分组的情况。

**Architecture:** 先用测试锁定边界行为，再仅调整 `peopleClusterThreshold` 常量。检测阈值 `MinConfidence` 保持不变。

**Tech Stack:** Go、GORM、testify

---

### Task 1: 写失败测试

**Files:**
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 添加边界测试**

- 预置一个已有人物，embedding 为 `[1, 0, 0]`
- 新样本 embedding 使用与其余弦相似度约 `0.89` 的向量
- 断言新样本应归入该已有人物

**Step 2: 运行测试确认失败**

Run:

```bash
cd backend && go test -run 'TestPeopleServiceCluster/中等相似度并入已有人物' -v ./internal/service
```

Expected: FAIL，现状会新建人物

### Task 2: 调整阈值

**Files:**
- Modify: `backend/internal/service/people_service.go`

**Step 1: 将 `peopleClusterThreshold` 从 `0.92` 改为 `0.88`**

**Step 2: 保持其他逻辑不变**

- 不改 `MinConfidence`
- 不改配置结构

### Task 3: 验证

**Files:**
- Test: `backend/internal/service/people_service_test.go`

**Step 1: 跑单测**

Run:

```bash
cd backend && go test -run 'TestPeopleServiceCluster/中等相似度并入已有人物' -v ./internal/service
```

Expected: PASS

**Step 2: 跑相关测试集**

Run:

```bash
cd backend && go test -run TestPeopleServiceCluster -v ./internal/service
```

Expected: PASS
