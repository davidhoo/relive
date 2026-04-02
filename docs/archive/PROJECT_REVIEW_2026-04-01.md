# 项目评审记录（2026-04-01）

> 范围：当前项目工作区评审，重点关注未提交改动。
> 评审时间：2026-04-01
> 结论类型：以缺陷、风险和回归为主，不含修复。

## 本次评审覆盖

- 未提交改动集中在以下链路：
  - 后端新增 `face/person/ml-service/sqlite-vec` 相关模型、仓储、服务、路由
  - 前端新增 `Faces` / `Persons` 页面与 API 调用
  - 展示链路增加人脸感知裁切
- 已执行的校验：
  - `cd backend && go test ./...`：通过
  - `cd frontend && npm run build`：失败

## 主要发现

### 1. 高优先级：人物页面当前会直接打断前端构建

- 位置：
  - `frontend/src/views/Persons/index.vue`
  - `frontend/src/views/Persons/Detail.vue`
- 问题：
  - 两处都把 `ElMessageBox.prompt()` 的返回值当成含 `value` 字段的对象来解构。
  - 当前 TypeScript 类型检查下，这里返回的是 `MessageBoxData`，不存在 `.value`。
- 证据：
  - `npm run build` 报错：
    - `src/views/Persons/Detail.vue(114,13): error TS2339: Property 'value' does not exist on type 'MessageBoxData'.`
    - `src/views/Persons/index.vue(98,13): error TS2339: Property 'value' does not exist on type 'MessageBoxData'.`
- 影响：
  - 当前未提交部分无法通过前端生产构建。

### 2. 高优先级：Go 后端与 Python ML 服务的人脸框协议不一致

- 位置：
  - `ml-service/app/schemas.py`
  - `ml-service/app/routers/face.py`
  - `backend/internal/mlclient/client.go`
  - `backend/internal/service/face_service.go`
- 问题：
  - Python 返回的 `bbox` 字段为 `x1/y1/x2/y2`。
  - Go 客户端定义的 `bbox` 字段为 `x/y/width/height`。
  - 后端保存时直接读取 Go 结构体中的 `X/Y/Width/Height`。
- 推断结果：
  - JSON 反序列化后，这些字段会落成零值。
  - 后续保存的人脸框、基于人脸框的人脸缩略图、裁切增强都会失真或失效。
- 影响：
  - 该链路可能“接口能通、数据错误”，属于高风险隐性缺陷。

### 3. 高优先级：`use_file_path=false` 分支下图片路径会被拼坏

- 位置：
  - `backend/internal/service/photo_scan_service.go`
  - `backend/internal/service/face_service.go`
- 问题：
  - 扫描阶段写入数据库的 `Photo.FilePath` 是绝对路径。
  - 检测阶段在非共享卷模式下又执行：
    - `filepath.Join(cfg.Photos.RootPath, photo.FilePath)`
- 结果：
  - 绝对路径会被重复拼接，形成类似：
    - `/app/photos/app/photos/...`
- 影响：
  - 文档中计划支持的 base64 / 远程 GPU 模式当前基本不可用。

### 4. 中优先级：人物详情页只能可靠展示前 100 个人物

- 位置：
  - `frontend/src/views/Persons/Detail.vue`
  - `backend/internal/api/v1/router/router.go`
- 问题：
  - 前端详情页没有按 ID 获取人物的接口。
  - 当前实现是固定请求 `listPersons(1, 100, false)` 后在前端本地查找。
  - 路由层当前只提供：
    - `GET /persons`
    - `GET /persons/family`
    - `GET /persons/:id/photos`
    - 更新/合并接口
  - 没有 `GET /persons/:id`。
- 影响：
  - 当人物数超过 100 时，从列表进入后面的条目时，详情页可能找不到对应人物。
  - 头部信息、重命名、家人标记状态会不稳定或缺失。

### 5. 中优先级：人物合并后 `family/has_family` 反范式可能变脏

- 位置：
  - `backend/internal/repository/person_repo.go`
  - `backend/internal/service/person_service.go`
- 问题：
  - 合并时只把 source 的人脸迁到 target，并删除 source 人物。
  - 之后仅在 `target.IsFamily == true` 时，才回写关联照片的 `has_family`。
  - 没有处理“source 是家人、target 不是家人”的一致性问题。
- 影响：
  - 合并后可能出现：
    - 家人人脸被并到非家人人物，但人物本身仍非家人
    - 照片 `has_family` 保持旧值，与真实关联关系不一致

## 补充观察

### 人脸检测页面文案与实际行为可能不一致

- 位置：
  - `frontend/src/views/Faces/index.vue`
  - `backend/internal/service/face_service.go`
  - `backend/internal/service/person_service.go`
- 现象：
  - 页面文案写的是“检测照片中的人脸，自动聚类为人物”。
  - 但当前检测流程里没有看到 `IncrementalCluster()` 被接入单张或后台检测主流程。
  - 当前只有手动触发“全量聚类”按钮。
- 风险：
  - 如果设计上 intended 是自动聚类，则实现缺失。
  - 如果设计上 intended 是手动聚类，则页面文案会误导用户。

## 建议处理顺序

1. 先修前端构建错误，恢复 `npm run build` 可用。
2. 统一 ML 服务与 Go 客户端的人脸框协议。
3. 修复非共享卷模式下的路径处理。
4. 增加 `GET /persons/:id`，替换详情页的“列表里找人”临时实现。
5. 明确人物合并时 `IsFamily` 与 `Photo.HasFamily` 的一致性规则，再补回写逻辑。

## 本次校验记录

### 后端

```bash
cd backend && go test ./...
```

- 结果：通过

### 前端

```bash
cd frontend && npm run build
```

- 结果：失败
- 直接错误：
  - `src/views/Persons/Detail.vue(114,13): error TS2339: Property 'value' does not exist on type 'MessageBoxData'.`
  - `src/views/Persons/index.vue(98,13): error TS2339: Property 'value' does not exist on type 'MessageBoxData'.`

