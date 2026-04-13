# People Detail Layout Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** 修复人物详情页列内卡片挤在一起的问题，并让人脸样本区域更紧凑。

**Architecture:** 在 `People/Detail.vue` 中复用公共 `section-stack` 容器承接列内纵向间距，同时把人脸样本网格从 3 列提升到 4 列并压缩卡片内边距。整体仍保持现有 `el-row + el-col` 双列结构。

**Tech Stack:** Vue 3、Element Plus、Scoped CSS、公共样式 `section-stack`

---

### Task 1: 写失败检查

**Files:**
- Test: `frontend/src/views/People/Detail.vue`

**Step 1: 运行结构检查**

Run:

```bash
node - <<'EOF'
import fs from 'node:fs'

const detail = fs.readFileSync('frontend/src/views/People/Detail.vue', 'utf8')

if ((detail.match(/section-stack/g) || []).length < 2) throw new Error('detail page missing section-stack wrappers')
if (!detail.includes('grid-template-columns: repeat(4, minmax(0, 1fr));')) throw new Error('face-grid is not 4-column on desktop')
EOF
```

**Step 2: 确认检查失败**

Expected: 报出缺少 `section-stack` 或桌面端网格列数不匹配

### Task 2: 调整模板结构

**Files:**
- Modify: `frontend/src/views/People/Detail.vue`

**Step 1: 左列加 `section-stack`**

- 包裹“人物信息”和“纠错操作”

**Step 2: 右列加 `section-stack`**

- 包裹“人脸样本”和“关联照片”

### Task 3: 缩小人脸样本

**Files:**
- Modify: `frontend/src/views/People/Detail.vue`

**Step 1: 调整 `face-grid` 列数**

- 桌面端 4 列
- 1200px 以下 3 列
- 768px 以下 2 列
- 480px 以下 1 列

**Step 2: 轻量压缩 `face-card`**

- 略减 `padding`
- 略减内部 `gap`
- 保持图片为正方形，不改按钮文案和交互

### Task 4: 验证

**Files:**
- Test: `frontend/src/views/People/Detail.vue`

**Step 1: 重新运行结构检查**

Run:

```bash
node - <<'EOF'
import fs from 'node:fs'

const detail = fs.readFileSync('frontend/src/views/People/Detail.vue', 'utf8')

if ((detail.match(/section-stack/g) || []).length < 2) throw new Error('detail page missing section-stack wrappers')
if (!detail.includes('grid-template-columns: repeat(4, minmax(0, 1fr));')) throw new Error('face-grid is not 4-column on desktop')
EOF
```

Expected: no output, exit 0

**Step 2: 运行前端构建**

Run:

```bash
cd frontend && npm run build
```

Expected: build succeeds
