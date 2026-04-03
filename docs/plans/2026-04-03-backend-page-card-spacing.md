# Backend Page Card Spacing Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 修复人物管理页卡片贴合问题，并统一人物页与 AI 分析页的纵向卡片间距模型。

**Architecture:** 使用公共 `section-stack` 容器类承接纵向间距，让卡片本身不再承担页面流中的垂直 `margin`。人物页在 tab 内容区内引入该容器，分析页则从 `margin-bottom` 迁移到同一模式。

**Tech Stack:** Vue 3、TypeScript、Element Plus、Vite、Scoped CSS、全局公共样式

---

### Task 1: 写结构回归检查

**Files:**
- Modify: `frontend/src/views/People/index.vue`
- Modify: `frontend/src/views/Analysis/index.vue`
- Modify: `frontend/src/assets/styles/common.css`

**Step 1: 写失败检查**

Run:

```bash
node - <<'EOF'
import fs from 'node:fs'

const common = fs.readFileSync('frontend/src/assets/styles/common.css', 'utf8')
const people = fs.readFileSync('frontend/src/views/People/index.vue', 'utf8')
const analysis = fs.readFileSync('frontend/src/views/Analysis/index.vue', 'utf8')

if (!common.includes('.section-stack')) throw new Error('missing section-stack in common.css')
if ((people.match(/section-stack/g) || []).length < 2) throw new Error('people page missing section-stack wrappers')
if (!analysis.includes('section-stack')) throw new Error('analysis page missing section-stack wrapper')
EOF
```

**Step 2: 运行检查确认失败**

Expected: 报出缺少 `section-stack` 相关结构

### Task 2: 实现公共栈容器

**Files:**
- Modify: `frontend/src/assets/styles/common.css`

**Step 1: 添加 `section-stack`**

- 新增统一纵向栈容器
- 使用 `display: flex`、`flex-direction: column`、`gap: 20px`

**Step 2: 保持实现最小化**

- 不引入额外视觉样式
- 只承载布局责任

### Task 3: 接入人物管理页

**Files:**
- Modify: `frontend/src/views/People/index.vue`

**Step 1: 给两个 tab 内容区增加 `section-stack` 包裹**

- “人物列表” tab：包住“筛选条件”和“人物列表”
- “后台任务” tab：包住“后台任务概览”、“队列统计”和“最近日志”

**Step 2: 保持现有 scoped 样式不变**

- 不新增局部 `margin-top`
- 继续复用现有 page gap 与 card padding

### Task 4: 接入 AI 分析页

**Files:**
- Modify: `frontend/src/views/Analysis/index.vue`

**Step 1: 用 `section-stack` 包住页面中的各个 `section-card`**

**Step 2: 去除 `.section-card` 的 `margin-bottom`**

- 让纵向间距完全由容器负责

### Task 5: 验证

**Files:**
- Test: `frontend/src/views/People/index.vue`
- Test: `frontend/src/views/Analysis/index.vue`
- Test: `frontend/src/assets/styles/common.css`

**Step 1: 重新运行结构检查**

Run:

```bash
node - <<'EOF'
import fs from 'node:fs'

const common = fs.readFileSync('frontend/src/assets/styles/common.css', 'utf8')
const people = fs.readFileSync('frontend/src/views/People/index.vue', 'utf8')
const analysis = fs.readFileSync('frontend/src/views/Analysis/index.vue', 'utf8')

if (!common.includes('.section-stack')) throw new Error('missing section-stack in common.css')
if ((people.match(/section-stack/g) || []).length < 2) throw new Error('people page missing section-stack wrappers')
if (!analysis.includes('section-stack')) throw new Error('analysis page missing section-stack wrapper')
EOF
```

Expected: no output, exit 0

**Step 2: 运行前端构建**

Run:

```bash
cd frontend && npm run build
```

Expected: build succeeds
