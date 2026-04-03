# People Detail Layout Design

**Goal:** 修复人物详情页左右列内 card 纵向贴合的问题，并降低人脸样本区域的单卡片尺寸，让样本区更紧凑。

**Scope:** `frontend/src/views/People/Detail.vue`

## Problem

当前人物详情页使用 `el-row + el-col` 两列布局，但每列内部的多个 `section-card` 直接顺排，没有中间栈容器，因此：

- 左列“人物信息 / 纠错操作”之间没有纵向间距
- 右列“人脸样本 / 关联照片”之间没有纵向间距

同时，人脸样本区当前为 3 列，卡片和图片都偏大，导致信息密度不足。

## Decision

使用已有的公共 `section-stack` 容器类统一列内卡片间距，并调整人脸样本网格密度：

- 左列内容包裹在一个 `section-stack`
- 右列内容包裹在一个 `section-stack`
- 人脸样本桌面端改为 4 列
- 中等宽度改为 3 列，平板 2 列，小屏 1 列
- 轻微减小人脸卡片 padding 和内部 gap

## Why This Approach

- 不改变详情页整体双列结构
- 直接复用已有的 `section-stack`，保持和人物列表页、分析页一致
- 人脸样本缩小通过网格密度和轻量卡片压缩完成，不会破坏按钮交互

## Visual Rules

- 列内 card 间距沿用 `section-stack` 的 `20px`
- 桌面端人脸样本：4 列
- 1200px 以下：3 列
- 768px 以下：2 列
- 480px 以下：1 列

## Verification

- 结构检查：
  - `People/Detail.vue` 中出现两个 `section-stack`
  - `face-grid` 使用 4 列
- 前端构建：
  - `cd frontend && npm run build`
