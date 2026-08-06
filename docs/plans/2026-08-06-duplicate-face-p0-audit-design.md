# P0 重复人脸跨人物归属审计：设计说明

## 目标

提供一次性、离线、只读的 P0 审计报告，找出“同一物理照片文件中的同一检测人脸，被归属到不同人物”的确定性冲突；报告必须提供可复核的照片 ID、人脸 ID 和人物 ID。

## 判定边界

P0 命中必须同时满足：

1. 两张或以上活动照片的 `file_hash` 相同；
2. 这些照片中的有效人脸 `embedding` 原始字节完全相同；
3. 相同 `(file_hash, SHA-256(embedding))` 组内至少有两个不同的非零 `person_id`；
4. 人脸不是 `cluster_status=excluded`，照片未删除且 `status=active`。

不满足上述全部条件的记录一律不作为 P0 输出。特别地：

- 一张合照出现多个不同人物是正常数据；
- 同一人物在重复照片中出现多次是正常数据；
- 余弦相似度高但 embedding 不完全相同属于后续 P2，不在本次范围；
- `same_photo_cooccurrence` 不是 P0 判定条件。

## 实现形态

新增独立 CLI：`backend/cmd/relive-duplicate-face-audit`。

它接收数据库副本路径，使用 SQLite `mode=ro`、`PRAGMA query_only=ON` 和单连接打开。只读保护沿用 `backend/cmd/relive-identity-profile-report` 的模式，但本工具允许输出 P0 复核所需的人名、照片 ID 和人脸 ID，因此不扩展现有身份画像校准报告。

命令接口：

```text
relive-duplicate-face-audit \
  -db /path/to/copied-relive.db \
  -format markdown|json \
  [-include-paths]
```

- `-db` 必填，必须是可读取的数据库副本；
- `-format` 默认 `markdown`；
- `-include-paths` 默认 `false`，只有显式开启才输出原始文件路径；
- 输出写至 stdout，调用方可分别重定向为 `.md`、`.json` 文件；工具不创建任何业务表或业务文件。

## 数据流

1. 单独聚合活动、未删除照片的重复 `file_hash` 数和涉及照片数，作为报告总览。
2. 仅流式读取这些重复哈希组中的有效、已归属人脸；SQL 按 `file_hash, photo_id, face_id` 排序。
3. 对每条非空 embedding 计算 `sha256.Sum256`；不输出 embedding 本身。
4. 以当前 `file_hash` 为窗口，在内存按 embedding fingerprint 聚合照片、人脸与人物证据。`file_hash` 变化时立即判定并释放前一组，避免全库 BLOB 自连接和全量常驻内存。
5. 当一组 fingerprint 的不同人物数不少于 2 时，生成一个 P0 冲突组。
6. 输出总览、覆盖/跳过统计与所有冲突组明细。

读取记录必须携带：`file_hash`、照片 ID/文件名/可选路径、face ID、人物 ID/名称、`manual_locked`、`manual_lock_reason`、`cluster_status`、更新时间和 embedding 原始字节。嵌入向量只在进程内取摘要，绝不进入 JSON/Markdown/日志。

## 报告契约

总览至少包含：

- 重复文件哈希组数、重复照片记录数；
- 读取的已归属有效人脸数；
- 缺失 embedding 而跳过的人脸数；
- P0 冲突组数、涉及人物数、涉及照片数和涉及人脸数。

每个冲突组至少包含：

- 完整 `file_hash` 与不可逆 embedding fingerprint；
- 去重后的人物 `(person_id, name)`；
- 每条证据的 `photo_id`、`face_id`、人物信息、文件名、锁定状态/原因、更新时间；
- 仅在 `-include-paths` 时出现的文件路径。

输出要求稳定排序：冲突组按 `file_hash`、fingerprint 升序；证据按 `photo_id`、`face_id` 升序；人物按 ID 升序。JSON 使用空数组/对象而非 `null`。

## 安全与非目标

- 审计不执行合并、移动、拆分、排除、cannot-link 写入、画像重建或建议刷新。
- 审计不修改 SQLite，不运行 migration，不要求 Relive 服务运行。
- 工具不自动判断“应归属哪个人物”；P0 只提供确定的重复归属证据，最终处置由人工完成。
- 不包含 P1（同 photo ID 的重复检测）、P2（近似 embedding）、前端页面、API、后台定时任务或批量修复。

## 验证原则

测试数据库必须覆盖：跨人物 P0 命中、同人物重复不命中、普通合照不命中、excluded 忽略、缺失 hash/embedding 的跳过计数、路径脱敏开关、JSON/Markdown 稳定排序，以及只读连接拒绝 INSERT/UPDATE/DELETE。

## 构建与运行

```bash
cd backend
go build -o bin/relive-duplicate-face-audit ./cmd/relive-duplicate-face-audit
./bin/relive-duplicate-face-audit -db /path/to/copied-relive.db -format markdown > duplicate-face-p0-audit.md
./bin/relive-duplicate-face-audit -db /path/to/copied-relive.db -format json > duplicate-face-p0-audit.json
./bin/relive-duplicate-face-audit -db /path/to/copied-relive.db -format markdown -include-paths > duplicate-face-p0-audit-with-paths.md
```

- `-db` 必须是数据库**副本**（先 `cp relive.db relive.copy.db` 再审计），避免读取正在被服务写入的库；
- 工具以 `mode=ro` + `PRAGMA query_only=ON` 打开，不会创建或修改任何数据；
- 默认不输出原始文件路径，仅在显式 `-include-paths` 时输出，便于脱敏分发；
- 报告只提供可复核证据（照片 ID、人脸 ID、人物 ID、embedding 指纹），最终归属处置由人工完成。
