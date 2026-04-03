# People Merge Avatar Candidates Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让人物详情页的 move/merge 候选列表只展示有头像的人物，并在下拉选项中直接显示头像。

**Architecture:** 后端在 `PersonResponse` 中显式提供 `has_avatar`，前端据此过滤候选项，并把 move/merge 两个选择器改为“头像 + 文本”自定义选项渲染。保持现有 API 请求结构与提交逻辑不变。

**Tech Stack:** Go、Gin、Vue 3、TypeScript、Element Plus、Vite

---

### Task 1: 为 `PersonResponse` 增加 `has_avatar`

**Files:**
- Modify: `backend/internal/model/dto.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/people_handler_test.go`

**Step 1: 写失败测试**

- 在 handler 测试中断言人物列表/人物详情返回 `has_avatar`
- `representative_face_id` 存在时返回 `true`
- `representative_face_id` 不存在时返回 `false`

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestPeopleHandler_(GetPeopleIncludesHasAvatar|GetPersonIncludesHasAvatar)' -v ./internal/api/v1/handler`

**Step 3: 实现最小后端改动**

- 在 `PersonResponse` 增加 `HasAvatar bool`
- 在 `personToResponse(...)` 中设置 `HasAvatar: person.RepresentativeFaceID != nil`

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestPeopleHandler_(GetPeopleIncludesHasAvatar|GetPersonIncludesHasAvatar)' -v ./internal/api/v1/handler`

### Task 2: 同步前端类型与候选过滤

**Files:**
- Modify: `frontend/src/types/people.ts`
- Modify: `frontend/src/views/People/Detail.vue`

**Step 1: 写最小前端验证改动**

- 在 `Detail.vue` 中先按 `has_avatar` 使用字段，制造类型缺失/构建失败前提
- 将候选列表过滤逻辑改为“排除当前人物 + `has_avatar === true`”

**Step 2: 跑构建确认失败**

Run: `cd frontend && npm run build`

Expected: 因 `Person.has_avatar` 缺失或相关类型不匹配而失败

**Step 3: 实现最小类型改动**

- 在 `Person` 类型中新增 `has_avatar: boolean`
- 保持现有候选排序逻辑不变

**Step 4: 跑构建确认通过**

Run: `cd frontend && npm run build`

### Task 3: 渲染头像化下拉选项

**Files:**
- Modify: `frontend/src/views/People/Detail.vue`

**Step 1: 写最小 UI 改动**

- 为 move/merge 两个 `el-select` 使用自定义 `el-option` 内容
- 每个候选项展示：
  - 头像：`/faces/{representative_face_id}/thumbnail`
  - 文本：`名称/编号 + 类别`

**Step 2: 跑构建验证**

Run: `cd frontend && npm run build`

Expected: PASS

**Step 3: 自检行为**

- 候选项不再是纯文本
- 没头像的人不会进入候选列表
- move/merge 提交逻辑未变化

### Task 4: 全量验证

**Files:**
- Test: `backend/internal/api/v1/handler/people_handler_test.go`
- Test: `frontend/src/views/People/Detail.vue`

**Step 1: 跑 handler**

Run: `cd backend && go test ./internal/api/v1/handler`

Expected: PASS

**Step 2: 跑前端构建**

Run: `cd frontend && npm run build`

Expected: PASS

**Step 3: 视情况跑 backend 全量**

Run: `cd backend && go test ./...`

Expected: PASS
