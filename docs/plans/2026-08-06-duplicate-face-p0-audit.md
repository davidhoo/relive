# P0 重复人脸跨人物归属审计 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 提供一个离线只读 CLI，报告同一物理照片文件中相同 embedding 被归属到不同人物的 P0 冲突，并列出可复核的照片、人脸和人物 ID。

**Architecture:** 新 CLI 直接使用 `database/sql` 与 SQLite 只读 URI 打开用户指定的数据库副本。它先识别重复文件哈希，再顺序流式读取相关人脸，在单个 file-hash 窗口内用 `SHA-256(embedding)` 聚合证据并输出跨人物冲突；不会在 SQLite 中做全库 BLOB 自连接，也不会写业务库。

**Tech Stack:** Go 1.25、`database/sql`、`github.com/mattn/go-sqlite3`、`crypto/sha256`、标准库 `encoding/json`。

---

### Task 1: 建立 CLI 外壳与只读数据库保护

**Files:**

- Create: `backend/cmd/relive-duplicate-face-audit/main.go`
- Create: `backend/cmd/relive-duplicate-face-audit/report.go`
- Create: `backend/cmd/relive-duplicate-face-audit/report_test.go`

**Step 1: 写失败测试。**

在测试中断言：缺失 `-db` 返回退出码 2；未知 `-format` 返回退出码 2；数据库不存在不会被创建；缺少 `photos`、`faces` 或 `people` 表返回明确错误。

**Step 2: 运行失败测试。**

Run: `cd backend && go test ./cmd/relive-duplicate-face-audit -run 'Test.*(Arg|Missing|NonExistent)' -v`

Expected: FAIL，因为包和 `run` 尚不存在。

**Step 3: 实现最小入口。**

- 实现 `run(args, stdout, stderr) int`；
- 解析 `-db`、`-format=markdown|json`、`-include-paths`；
- 参照 `cmd/relive-identity-profile-report/main.go` 的 `openReadOnly`，使用 `file:<abs>?mode=ro&_query_only=true&_busy_timeout=60000`、`PRAGMA query_only=ON` 和 `SetMaxOpenConns(1)`；
- 校验三张必需表存在；
- usage 明确数据库必须是副本，工具不会写库。

**Step 4: 验证。**

Run: `cd backend && go test ./cmd/relive-duplicate-face-audit -run 'Test.*(Arg|Missing|NonExistent)' -v`

Expected: PASS。

**Step 5: 提交。**

```bash
git add backend/cmd/relive-duplicate-face-audit/main.go backend/cmd/relive-duplicate-face-audit/report.go backend/cmd/relive-duplicate-face-audit/report_test.go
git commit -m "feat(audit): add read-only duplicate face report CLI"
```

### Task 2: 实现重复文件子集的流式 P0 聚合

**Files:**

- Modify: `backend/cmd/relive-duplicate-face-audit/report.go`
- Modify: `backend/cmd/relive-duplicate-face-audit/report_test.go`

**Step 1: 写失败测试。**

构建最小 SQLite fixture，包含：

- 两张活动照片，`file_hash` 相同；
- 两张有效人脸，embedding 字节相同、人物不同；
- 两个 `people` 行。

断言 `buildReport` 产生一个 P0 冲突组，组内列出两张照片 ID、两个人脸 ID 和两个人物 ID；并断言 report 对象和 JSON 中不出现原始 embedding。

**Step 2: 运行失败测试。**

Run: `cd backend && go test ./cmd/relive-duplicate-face-audit -run TestP0CrossPersonExactEmbeddingReported -v`

Expected: FAIL，因为尚未采集证据。

**Step 3: 实现聚合。**

- 增加 `Report`、`Summary`、`ConflictGroup`、`Evidence` 和 `PersonRef`；所有 slice/map 初始化为空，禁止 JSON `null`；
- 先执行轻量聚合，获取重复哈希组数与重复照片记录数；
- 使用 CTE 仅选取 `photos.status='active'`、`deleted_at IS NULL`、非空 `file_hash` 的重复 hash，并 join 有效、已归属、未 excluded 的 faces 与 people；
- 查询按 `p.file_hash, p.id, f.id` 排序；当 hash 变化，计算并输出前一 hash 窗口；
- 对非空 `f.embedding` 使用 `sha256.Sum256`，以 `(file_hash, fingerprint)` 聚合；组内不同 `person_id >= 2` 时输出；
- 记录空 embedding 的跳过数；不执行任何 INSERT/UPDATE/DELETE。

**Step 4: 验证。**

Run: `cd backend && go test ./cmd/relive-duplicate-face-audit -run TestP0CrossPersonExactEmbeddingReported -v`

Expected: PASS。

**Step 5: 提交。**

```bash
git add backend/cmd/relive-duplicate-face-audit/report.go backend/cmd/relive-duplicate-face-audit/report_test.go
git commit -m "feat(audit): detect cross-person duplicate face evidence"
```

### Task 3: 固化误报边界、路径脱敏和稳定输出

**Files:**

- Modify: `backend/cmd/relive-duplicate-face-audit/report.go`
- Modify: `backend/cmd/relive-duplicate-face-audit/report_test.go`

**Step 1: 写失败测试。**

分别构造并断言：

- 同一人物的重复文件/相同 embedding 不产生冲突；
- 同一照片含两张不同 embedding、两个不同人物（普通合照）不产生冲突；
- `cluster_status='excluded'` 的人脸不产生冲突；
- 缺失 hash 或 embedding 只增加跳过统计；
- 默认 Markdown/JSON 不含 `file_path`，开启 `-include-paths` 后才含路径；
- 多次运行输出的冲突组、人物和证据排序一致。

**Step 2: 运行失败测试。**

Run: `cd backend && go test ./cmd/relive-duplicate-face-audit -run 'Test(P0|Excluded|Missing|Path|Stable)' -v`

Expected: FAIL，直到过滤与格式化完整实现。

**Step 3: 实现最小输出与过滤。**

- 按设计说明实现 active/deleted/excluded 过滤与跳过统计；
- 只输出 embedding fingerprint，绝不输出 embedding 字节或其 base64/hex 形式；
- 实现 Markdown 总览与逐组明细、JSON 完整结构；
- 在 render 层根据 `includePaths` 清除路径字段，避免默认输出泄露路径；
- 采用 file hash → fingerprint → photo ID/face ID/person ID 的确定性排序。

**Step 4: 验证。**

Run: `cd backend && go test ./cmd/relive-duplicate-face-audit -v`

Expected: PASS。

**Step 5: 提交。**

```bash
git add backend/cmd/relive-duplicate-face-audit/report.go backend/cmd/relive-duplicate-face-audit/report_test.go
git commit -m "test(audit): cover P0 filtering and report privacy"
```

### Task 4: 验证只读性、构建和实际运行说明

**Files:**

- Modify: `backend/cmd/relive-duplicate-face-audit/report_test.go`
- Modify: `docs/plans/2026-08-06-duplicate-face-p0-audit-design.md`

**Step 1: 写失败测试。**

在 fixture 上记录业务表行数，调用报告构建后断言行数不变；单独从同一 `mode=ro` 连接执行 INSERT、UPDATE、DELETE，均必须失败。

**Step 2: 运行失败测试。**

Run: `cd backend && go test ./cmd/relive-duplicate-face-audit -run 'Test.*ReadOnly|Test.*BusinessTablesUnchanged' -v`

Expected: FAIL，直到只读断言与测试 helper 完成。

**Step 3: 完成验证和使用说明。**

- 完成只读性测试；
- 在设计说明末尾补充构建与运行命令：

```bash
cd backend
go build -o bin/relive-duplicate-face-audit ./cmd/relive-duplicate-face-audit
./bin/relive-duplicate-face-audit -db /path/to/copied-relive.db -format markdown > duplicate-face-p0-audit.md
./bin/relive-duplicate-face-audit -db /path/to/copied-relive.db -format json > duplicate-face-p0-audit.json
```

**Step 4: 全量验证。**

Run: `cd backend && go test ./cmd/relive-duplicate-face-audit -v && go build ./cmd/relive-duplicate-face-audit`

Expected: PASS，且构建不产生运行时数据库文件。

**Step 5: 提交。**

```bash
git add backend/cmd/relive-duplicate-face-audit/report_test.go docs/plans/2026-08-06-duplicate-face-p0-audit-design.md
git commit -m "docs(audit): document P0 duplicate face report workflow"
```

## 不包含

- 自动合并、移动、拆分、排除或创建 cannot-link；
- 修改人物聚类、identity profile、merge suggestion 分数或阈值；
- 同 photo ID 的重复检测（P1）；
- 高相似但非完全相同 embedding 的检测（P2）；
- API、前端页面、定时任务、线上数据库直接扫描或部署流程。
