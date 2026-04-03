# People Merge Avatar Candidates Design

**Goal:** 在人物详情页的“移动到其他人物”和“合并其他人物到当前人物”选择器中，只展示有头像的人物，并让候选项直接显示头像，避免用户只能看到“未命名人物 #310 · 路人”这类难以辨认的文本。

**Scope:** `backend/internal/model/dto.go`、`backend/internal/api/v1/handler/people_handler.go`、`frontend/src/types/people.ts`、`frontend/src/views/People/Detail.vue`；本次不修改 merge/move API 请求结构，不改其它页面的人物选择器。

## Current Problem

当前人物详情页的两个选择器：

- 移动到其他人物
- 合并其他人物到当前人物

都直接使用 `peopleApi.getList(...)` 返回的人物列表，并在前端以纯文本下拉显示：

- `未命名人物 #310 · 路人`

这有两个问题：

1. **无法辨认候选对象**
   - 用户执行合并或移动时，核心判断依据往往是头像，而不是 ID 或类别
   - 纯文本列表在大量“未命名人物”场景下几乎不可用

2. **前端隐式猜测“有没有头像”**
   - 当前前端只能看到 `representative_face_id`
   - 如果要过滤头像候选，只能在前端自己推断
   - 这种语义不够明确，也不利于其它页面复用

## Alternatives Considered

### Option 1: 前端继续根据 `representative_face_id` 自行过滤

优点：
- 改动最小

缺点：
- “有头像”语义仍然由前端猜测
- 后续其它页面若要复用，会重复同一逻辑
- DTO 层没有明确表达能力

### Option 2: 后端在 `PersonResponse` 中显式返回 `has_avatar`

优点：
- 语义清晰，前后端职责明确
- 前端只关心“这个人物是否适合用于头像候选”
- 后续其它页面可直接复用

缺点：
- 需要同步修改 DTO、handler 和前端类型

### Option 3: 后端进一步检查头像缩略图文件是否真实存在

优点：
- `has_avatar` 语义最严格

缺点：
- 需要 handler 层访问文件系统
- 列表接口会引入额外 I/O
- 超出本次需求边界

**Decision:** 采用 Option 2。

## Design

### Backend Contract

在 `PersonResponse` 中新增：

- `has_avatar: boolean`

计算规则保持简单、稳定：

- `representative_face_id != nil` → `has_avatar = true`
- 否则 `false`

本次不检查缩略图文件是否真实存在，因为：

- 当前系统已经把“是否有人物头像”建模为 `representative_face_id`
- 文件存在性属于资源缓存/生成问题，不应绑进列表 DTO

### Frontend Behavior

`People/Detail.vue` 中的候选人物列表改为：

- 先排除当前人物自己
- 再过滤 `has_avatar === true`
- 再按现有排序逻辑展示

两个选择器都使用同一套候选列表：

- “移动到其他人物”
- “合并其他人物到当前人物”

### Candidate Rendering

下拉选项从纯文本改为：

- 左侧头像：使用 `representative_face_id` 拼接现有 `/faces/{id}/thumbnail` URL
- 右侧文字：保留 `名称/编号 + 类别`

这样既能靠头像识别，也不会丢失搜索和文本确认能力。

### Non-Goals

本次不做：

- 不修改后端 merge/move 接口
- 不新增独立头像预览接口
- 不处理没有头像但仍想合并的特殊流程
- 不统一改造其它页面的人物下拉

## Testing Strategy

### Backend

通过 handler 层测试验证：

- `PersonResponse` 返回 `has_avatar`
- 有 `representative_face_id` 时为 `true`
- 没有时为 `false`

### Frontend

前端当前没有独立测试框架，本次用两层验证：

- 类型检查：新增 `Person.has_avatar`
- 构建验证：`cd frontend && npm run build`

### Manual Expectation

在人物详情页中：

- 候选项必须带头像
- 没头像的人物不再出现在 move/merge 下拉里
- 现有 move/merge 提交流程保持不变
