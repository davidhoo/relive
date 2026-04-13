# People List Cards Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** 将人物管理页的人物列表从表格改为高密度卡片网格，提升空间利用率。

**Architecture:** 仅修改 `People/index.vue` 的展示层，将列表区域从 `el-table` 替换为响应式人物卡片网格。数据加载、筛选、分页和详情跳转逻辑保持不变。

**Tech Stack:** Vue 3、Element Plus、Scoped CSS

---

### Task 1: 写失败检查

**Files:**
- Test: `frontend/src/views/People/index.vue`

**Step 1: 运行结构检查**

Run:

```bash
node - <<'EOF'
import fs from 'node:fs'

const page = fs.readFileSync('frontend/src/views/People/index.vue', 'utf8')

if (page.includes('<el-table')) throw new Error('people list still uses table')
if (!page.includes('people-card-grid')) throw new Error('people card grid missing')
EOF
```

**Step 2: 确认检查失败**

Expected: 报出仍在使用表格

### Task 2: 替换模板结构

**Files:**
- Modify: `frontend/src/views/People/index.vue`

**Step 1: 移除 `el-table` 列表**

**Step 2: 改为人物卡片网格**

- 头像
- 姓名
- `#ID`
- 类别标签
- 照片数
- 人脸数
- 查看详情按钮

### Task 3: 添加样式

**Files:**
- Modify: `frontend/src/views/People/index.vue`

**Step 1: 增加卡片网格样式**

- 响应式 `grid`
- 紧凑卡片
- hover 态
- 卡片内统计区

**Step 2: 清理不再需要的表格样式**

### Task 4: 验证

**Files:**
- Test: `frontend/src/views/People/index.vue`

**Step 1: 重新运行结构检查**

Run:

```bash
node - <<'EOF'
import fs from 'node:fs'

const page = fs.readFileSync('frontend/src/views/People/index.vue', 'utf8')

if (page.includes('<el-table')) throw new Error('people list still uses table')
if (!page.includes('people-card-grid')) throw new Error('people card grid missing')
EOF
```

Expected: no output, exit 0

**Step 2: 运行前端构建**

Run:

```bash
cd frontend && npm run build
```

Expected: build succeeds
