# Factory Reset Design

**Date:** 2026-04-02

> **Status:** Completed
> **Note:** The factory reset flow described here has landed on `main`; keep this document for historical traceability.

## Problem

当前系统还原依赖手写表名单清库。随着业务表、任务表、事件表、`photo_tags`、FTS 虚拟表和触发器持续增加，这个方案天然会漏，维护成本会越来越高。

同时，系统运行时还有 scheduler、结果队列和后台任务。在线直接删表或删 SQLite 文件都容易和活跃连接、后台写入竞争。

## Goal

把“系统还原”改成真正的恢复出厂设置：

- 不再维护表白名单
- 不依赖运行中数据库连接做逐表删除
- 通过删除 SQLite 文件和派生目录回到全新初始状态
- 启动后自动重新建库并恢复默认 admin 用户

## Chosen Approach

采用“两阶段恢复出厂设置”：

1. 运行中收到 `/system/reset` 请求时，只做校验、写入 reset 标记文件，并返回成功响应。
2. 响应发送后，当前进程主动退出。
3. 下次启动时，在打开数据库之前检查 reset 标记。
4. 若标记存在，删除：
   - `relive.db`
   - `relive.db-wal`
   - `relive.db-shm`
   - 缩略图目录内容
   - 展示批次目录内容
   - 缓存目录内容
5. 删除完成后移除标记文件，继续正常启动。
6. 启动流程中的 `AutoMigrate` 和默认用户初始化会自动重建系统。

## Why This Approach

- 和 schema 解耦。新增表、索引、FTS、trigger 后无需同步修改 reset 逻辑。
- 删除发生在数据库尚未打开之前，避免 SQLite 活跃连接与 WAL 状态不一致。
- 对 Docker 部署友好。当前 compose 已配置 `restart: unless-stopped`，进程退出后容器会自动拉起。
- 即使退出前还有少量后台写入，也不会影响最终结果，因为数据库文件会在下次启动前整体删除。

## Non-Goals

- 不做数据保留模式
- 不增加二次密码确认
- 不支持 PostgreSQL 的恢复出厂设置
- 不实现复杂的重启进度页或健康轮询 UI

## Runtime Behavior

### Reset Request Phase

- 要求 `confirm_text = RESET`
- 为 SQLite 配置写入 reset 标记文件
- 返回“已安排恢复出厂设置，服务即将重启”
- 启动一个短延迟 goroutine 退出当前进程

### Startup Cleanup Phase

- 在 `main` 中数据库初始化前运行
- 检查 reset 标记文件
- 顺序删除数据库文件和派生目录内容
- 删除标记文件
- 记录日志
- 继续原有启动流程

## Failure Handling

- 如果写入标记文件失败，接口直接返回错误，不退出进程。
- 如果启动时清理失败，保留标记文件并返回启动错误，避免系统在半清理状态下继续运行。
- 如果不是 SQLite，接口返回明确错误，避免伪成功。

## Files Expected To Change

- `backend/cmd/relive/main.go`
- `backend/internal/api/v1/handler/system.go`
- `backend/internal/api/v1/handler/system_test.go`
- `backend/internal/model/dto.go`
- `frontend/src/types/system.ts`
- `frontend/src/views/System/index.vue`
- 新增一个独立的 reset 工具包及其测试

## Verification

- 单元测试：reset 标记文件存在时，启动清理会删除 DB/WAL/SHM 与派生目录内容
- 单元测试：无标记时不会误删文件
- Handler 测试：`/system/reset` 成功响应后会安排退出流程
- 后端测试：`go test ./internal/api/v1/handler ./internal/factoryreset`
