# People Rescan By Path Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** 在扫描路径列表中增加“人物重扫”按钮，点击后对该路径重新入队人物任务并自动启动人物后台。

**Architecture:** 后端新增一个路径级人物重扫接口，由 handler 串联 `StartBackground` 与 `EnqueueByPath`。前端在照片管理页新增按钮并调用该接口。人物管理页不新增入口。

**Tech Stack:** Go、Gin、Vue 3、Element Plus、Axios

---

### Task 1: 写后端失败测试

**Files:**
- Modify: `backend/internal/api/v1/handler/people_handler_test.go`

**Step 1: 添加 handler 测试**

- 请求：`POST /api/v1/people/rescan-by-path`
- 断言：
  - 会调用 `StartBackground`
  - 会调用 `EnqueueByPath`
  - 传入正确路径
  - 返回成功响应和入队数量

**Step 2: 运行测试确认失败**

Run:

```bash
cd backend && go test -run TestPeopleHandlerRescanByPath -v ./internal/api/v1/handler
```

Expected: FAIL，因 handler/接口尚未实现

### Task 2: 实现后端接口

**Files:**
- Modify: `backend/internal/model/dto.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/router/router.go`

**Step 1: 增加请求 DTO**

- 新增路径请求结构体

**Step 2: 增加 handler**

- 绑定路径
- 若任务 `stopping`，返回冲突
- 若后台未运行，先启动
- 调用 `EnqueueByPath`
- 返回 `count` 与 `background_started`

**Step 3: 注册路由**

### Task 3: 写前端失败检查

**Files:**
- Test: `frontend/src/views/Photos/index.vue`
- Test: `frontend/src/api/people.ts`

**Step 1: 运行结构检查**

Run:

```bash
node - <<'EOF'
import fs from 'node:fs'

const page = fs.readFileSync('frontend/src/views/Photos/index.vue', 'utf8')
const api = fs.readFileSync('frontend/src/api/people.ts', 'utf8')

if (!page.includes('人物重扫')) throw new Error('people rescan button missing')
if (!page.includes('handlePeopleRescanPath')) throw new Error('people rescan handler missing')
if (!api.includes('rescanByPath')) throw new Error('people api rescanByPath missing')
EOF
```

Expected: FAIL

### Task 4: 实现前端按钮

**Files:**
- Modify: `frontend/src/api/people.ts`
- Modify: `frontend/src/views/Photos/index.vue`

**Step 1: 增加 API 方法**

- `peopleApi.rescanByPath(path)`

**Step 2: 增加按钮和处理函数**

- 放在扫描路径操作列
- 路径禁用时禁用
- 显示 loading
- 成功提示入队数量

### Task 5: 验证

**Files:**
- Test: `backend/internal/api/v1/handler/people_handler_test.go`
- Test: `frontend/src/views/Photos/index.vue`
- Test: `frontend/src/api/people.ts`

**Step 1: 跑后端测试**

Run:

```bash
cd backend && go test -run TestPeopleHandlerRescanByPath -v ./internal/api/v1/handler
```

Expected: PASS

**Step 2: 跑前端结构检查**

Run:

```bash
node - <<'EOF'
import fs from 'node:fs'

const page = fs.readFileSync('frontend/src/views/Photos/index.vue', 'utf8')
const api = fs.readFileSync('frontend/src/api/people.ts', 'utf8')

if (!page.includes('人物重扫')) throw new Error('people rescan button missing')
if (!page.includes('handlePeopleRescanPath')) throw new Error('people rescan handler missing')
if (!api.includes('rescanByPath')) throw new Error('people api rescanByPath missing')
EOF
```

Expected: PASS

**Step 3: 跑前端构建**

Run:

```bash
cd frontend && npm run build
```

Expected: build succeeds
