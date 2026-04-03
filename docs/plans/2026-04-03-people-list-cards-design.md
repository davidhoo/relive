# People List Cards Design

**Goal:** 将人物管理页中的人物列表从表格改为高密度多列卡片列表，提升空间利用率。

**Scope:** `frontend/src/views/People/index.vue`

## Problem

当前人物列表使用 `el-table`，虽然信息完整，但横向留白较多，导致人物管理页在宽屏下空间利用率低。

## Decision

将人物列表切换为极简卡片网格：

- 保留筛选卡与分页
- 将人物展示改为响应式卡片网格
- 每张卡片只显示高价值信息：
  - 头像
  - 姓名
  - `#ID`
  - 类别
  - 照片数
  - 人脸数
  - “查看详情”
- 去掉“最近更新”等次要信息

## Why This Approach

- 直接提升一屏可展示的人物数量
- 视觉上更接近人物库而不是后台数据表
- 仍保留类别和计数，不牺牲关键信息
- 筛选逻辑、分页逻辑、详情跳转逻辑都无需变更

## Layout Rules

- 使用响应式卡片网格
- 卡片支持整卡点击进入详情
- 保留单独“查看详情”按钮，提升交互可发现性
- 手机端降为单列或双列，避免压缩过度

## Verification

- 结构检查：
  - `People/index.vue` 不再包含 `el-table`
  - 存在人物卡片网格结构
- 前端构建：
  - `cd frontend && npm run build`
