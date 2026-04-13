# Backend Page Card Spacing Design

> **Status:** Completed
> **Note:** The spacing/layout changes described here have landed on `main`; keep this document for historical traceability.

**Goal:** 统一后台页面纵向卡片之间的间距模型，修复人物管理页中 tab 内容区卡片挤在一起的问题，并让同类页面使用一致的布局方式。

**Scope:** `frontend/src/views/People/index.vue`、`frontend/src/views/Analysis/index.vue`、`frontend/src/assets/styles/common.css`

## Problem

当前人物管理页的多个 `el-card` 直接堆叠在 `el-tab-pane` 下，没有中间栈容器提供垂直间距，因此：

- “筛选条件”和“人物列表”卡片贴在一起
- “后台任务概览”、“队列统计”、“最近日志”卡片贴在一起

与此同时，分析页仍然依赖单卡片 `margin-bottom` 维持纵向节奏，和其他后台页使用 `gap` 的方式不一致。

## Decision

采用统一的“纵向卡片栈”模型：

- 在公共样式中新增可复用的 `section-stack`
- 由容器负责卡片之间的间距，卡片自身不承担纵向 `margin`
- 人物管理页的每个 tab 内容区使用 `section-stack`
- AI 分析页也改为使用 `section-stack`，去掉 `.section-card` 的 `margin-bottom`

## Why This Approach

- 改动面小，只触及真正有问题的页面
- 不影响已经正常依赖页面根节点 `gap` 的页面
- 后续新增后台页时，可以直接复用同一个栈容器类
- 避免继续混用 `gap` 和 `margin-bottom` 两套纵向布局逻辑

## Visual Rules

- 卡片垂直间距统一为 `20px`
- 移动端不额外引入新的缩进规则，沿用页面现有 card padding 响应式设置
- 不改变卡片边框、圆角、阴影和 header/body padding

## Verification

- 一次性结构检查：
  - `People/index.vue` 两个 tab 内容区都包裹在 `section-stack` 中
  - `Analysis/index.vue` 使用 `section-stack`
  - `common.css` 中存在 `section-stack` 定义
- 前端类型检查与构建：
  - `cd frontend && npm run build`
