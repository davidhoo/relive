# NAS 备份工具

通过开发机一条命令，在 NAS 上创建**已校验的 SQLite 在线备份**，无需停止或重启 Relive 容器。

```bash
make backup-nas
```

> 本工具仅负责**备份**。部署（`make deploy` / `make deploy-image`）与恢复是独立、显式的操作，备份工具不会自动触发它们。

---

## 设计目标与非目标

**目标**

- 一条可重复执行的命令，连接 NAS 并创建已校验的 SQLite 在线备份包。
- 不复制活动数据库文件（`cp` 活库是禁止的）。
- 不把数据库下载到开发机。
- 备份包目录权限 `0700`、文件权限 `0600`。

**非目标**

- 不会从 `deploy` / `deploy-image` 自动调用。
- 不会停止、重启、暂停或修改 Relive 容器。
- 不提供一键恢复或自动回滚。
- 除非显式启用保留策略，否则不删除旧备份。
- 终端输出永不包含 secret 值。

完整设计见 `docs/plans/2026-07-03-nas-backup-tool-design.md`。

---

## 配置

使用专用的本地配置文件，而不是应用的 `.env`：

```text
.nas-backup.env          # 真实本地值；Git 忽略
.nas-backup.env.example  # 占位符模板；Git 跟踪
```

首次使用：

```bash
cp .nas-backup.env.example .nas-backup.env
# 然后编辑 .nas-backup.env，填入真实值
```

`.nas-backup.env.example` 仅含占位符，不含真实主机名、用户名、密钥或 password。

### 支持的变量与优先级

优先级从高到低：

1. 命令附带的环境变量（进程环境）。
2. `.nas-backup.env` 文件中的值。
3. 路径与保留数量的脚本默认值。

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `RELIVE_NAS_HOST` | SSH 目标，如 `user@nas`。无个人默认值，必须提供。 | （无） |
| `RELIVE_NAS_ROOT` | NAS 上 Relive 安装根目录。 | `/volume1/docker/relive` |
| `RELIVE_NAS_DB` | 活动数据库路径。默认从 root 派生。 | `$RELIVE_NAS_ROOT/data/backend/relive.db` |
| `RELIVE_NAS_BACKUP_DIR` | 备份输出根目录。默认从 root 派生。 | `$RELIVE_NAS_ROOT/backup` |
| `RELIVE_BACKUP_KEEP` | 保留的最新成功备份目录数，`0` 表示不自动删除。 | `0` |
| `RELIVE_BACKUP_LABEL` | 本次备份标签，按调用传入。 | `manual` |

加载器只接受上述 `RELIVE_*` 键与简单 `KEY=value` 语法，**不** `source` 任意 shell 代码。`export`、命令替换、反引号、未知键、重复键、缩进行都会被拒绝。

`RELIVE_NAS_HOST` 没有个人跟踪默认值，必须由环境或 `.nas-backup.env` 提供。SSH 认证沿用开发机既有的 SSH 配置与密钥；password 与私钥永不存入仓库。

`RELIVE_BACKUP_LABEL` 通常按调用传入：

```bash
RELIVE_BACKUP_LABEL=pre-task14 make backup-nas
```

标签会先按保守字符集与长度限制归一化，再传给 NAS（`[A-Za-z0-9][A-Za-z0-9._-]{0,49}`）。

---

## 使用示例

```bash
# 默认（标签 manual，保留 0）
make backup-nas

# 带标签
RELIVE_BACKUP_LABEL=pre-task14 make backup-nas

# 保留最新 5 个成功备份
RELIVE_BACKUP_KEEP=5 make backup-nas

# 临时覆盖主机（不修改 .nas-backup.env）
RELIVE_NAS_HOST=admin@nas2 make backup-nas
```

成功输出简洁：

```text
Backup complete
Directory: /volume1/docker/relive/backup/2026-07-03-103000-pre-task14
Database quick_check: ok
Checksums: verified
```

---

## 备份包内容

成功输出目录：`<backup-root>/YYYY-MM-DD-HHMMSS-<label>/`

```text
relive.db
schema.sql
config.tar.gz
repository.bundle
git-status.txt
runtime.txt
manifest.txt
SHA256SUMS
RESTORE.txt
```

- **`relive.db`**：仅用 SQLite 在线备份命令 `.backup` 生成。绝不 `cp` 活库、`-wal`、`-shm`。
- **`schema.sql`**：从完成的备份数据库导出（不是活动库）。
- **`config.tar.gz`**：仅包含白名单中存在的文件：`.env`、`backend/config.prod.yaml`、`docker-compose.yml`、`docker-compose.prod.yml`、`VERSION`。**包含 secret，因此文件权限 `0600`**。`.nas-backup.env` 是开发机传输配置，刻意排除。
- **`repository.bundle`**：`git bundle create ... --all`，仅捕获已提交 refs。`git-status.txt` 记录分支、HEAD、porcelain 状态，**不**捕获未提交 diff，避免意外归档含 secret 的补丁。
- **`runtime.txt`**：仅含清洗后的运行时元数据：主机名、备份时间戳、Relive 容器名与镜像 ID、健康/运行状态、启动时间、重启次数、Compose 状态、仓库 HEAD 与应用版本。**不含** Docker 环境变量、完整 `docker inspect` 输出、挂载文件内容、token、API key。
- **`manifest.txt`**：记录来源路径、文件大小、SQLite 校验结果、Git commit、工具版本、完成时间戳。**不含 secret 值**。
- **`RESTORE.txt`**：手动恢复清单，显著声明恢复前须先停止 Relive。备份工具自身绝不执行恢复命令。

权限：目录 `0700`，普通文件 `0600`。`SHA256SUMS` 覆盖除自身外的所有包文件，发布前立即 `sha256sum -c` 校验。

---

## 备份与校验流程

1. 校验参数与依赖（`sqlite3`、`git`、`docker`、`tar`、`sha256sum`）。
2. 获取备份锁（原子 `mkdir`，并发备份立即失败，绝不猜测/删除他人锁）。
3. 确认源数据库与仓库存在。
4. 写入前检查可用空间（至少活动库大小 + `max(20% 库大小, 256MiB)`）。
5. 创建 `0700`、名称以 `.partial` 结尾的目录。
6. 用 SQLite `.backup` 写入该目录。
7. 对备份执行 `PRAGMA quick_check`，要求恰好 `ok`。
8. 从备份导出 schema。
9. 创建配置归档、Git bundle、status、runtime 元数据、manifest、恢复说明。
10. 目录设 `0700`，普通文件设 `0600`。
11. 为除 `SHA256SUMS` 外的每个包文件生成校验和。
12. 立即校验所有校验和。
13. 原子地将 `.partial` 目录改名为最终名。
14. 仅在新备份完全校验通过后，可选地执行保留策略。
15. 通过 trap 在成功与失败时都释放锁。

任一步骤失败，最终目录永不创建；worker 删除自己的未完成目录并返回非零，**绝不删除先前成功的备份**。

---

## 保留策略

默认 `RELIVE_BACKUP_KEEP=0`，即**不自动删除**。

设为正整数时，保留策略**仅在新备份成功后**运行：

- 只考虑位于备份根**直接**子目录、名称完全匹配工具时间戳-标签模式的目录。
- 按时间戳排序，保留最新 N 个。
- 忽略符号链接、普通文件、`.partial` 目录、手动命名的目录、备份根之外的路径。
- **永不删除刚创建的备份**。
- 删除失败报错，但不会使新完成的备份失效或被删除。

---

## 安全

- `.nas-backup.env` 被 Git 忽略；`.nas-backup.env.example` 仅含占位符。
- SSH 使用既有密钥/配置与非交互模式（`BatchMode=yes`、有限超时）。
- 所有路径与标签均经校验并 shell 引用。
- 运行时采集使用显式 Docker 格式字符串，而非完整 inspect 输出。
- 配置归档与数据库文件权限 `0600`；包目录 `0700`。
- 终端输出永不包含 `.env` 或 YAML 内容。
- 工具绝不调用恢复、容器重启、迁移或部署。

---

## 手动恢复（前置条件与验证）

> 恢复**必须先停止 Relive**。不要在运行中的部署上恢复；SQLite 在线备份只在后端停止时才可安全替换。

完整步骤见备份包内的 `RESTORE.txt`，要点：

1. SSH 到 NAS。
2. 停止 Relive：`docker compose -f <nas-root>/docker-compose.prod.yml down`
3. 替换前先备份当前活动数据库。
4. 校验备份：
   ```bash
   sqlite3 -readonly <backup-dir>/relive.db 'PRAGMA quick_check;'
   sha256sum -c <backup-dir>/SHA256SUMS
   ```
5. 恢复数据库（示例）：
   ```bash
   cp <backup-dir>/relive.db <nas-root>/data/backend/relive.db
   rm -f <nas-root>/data/backend/relive.db-wal <nas-root>/data/backend/relive.db-shm
   ```
6. 重启 Relive：`docker compose -f <nas-root>/docker-compose.prod.yml up -d`

`config.tar.gz` 含 secret，请以与 `.env` 同等的谨慎处理。

---

## v2 验证器恢复的 preflight/restore 引用

人脸质检 v2（`independent_v2`）恢复涉及 YuNet 验证器资产与 rescore run #3 的受控恢复，完整 runbook 见
`docs/plans/2026-08-13-face-quality-v2-recovery-and-pause-race-repair.md` 的「实施与上线记录」段。本工具与之衔接的要点：

- **备份时机**：在执行条件 SQL 把 run #3 落为 `paused` **之前**，先用 `make backup-nas` 生成已校验备份。备份是回滚的唯一安全来源。
- **恢复前置条件**：恢复**必须先停止 `relive` 后端**（见上节）。v2 恢复额外要求模型缺失/摘要失败时**不得回退 v1、不得恢复或 enforce #3**——仅在明确授权下用已校验备份恢复。
- **不替代构建期校验**：YuNet 模型缺失或摘要失败时，不得用数据库备份/恢复绕过 `scripts/fetch-yunet-model.sh` + Dockerfile `sha256sum -c` 的构建期门禁。

---

## 故障排查

| 现象 | 排查 |
|------|------|
| SSH 连接失败 | 确认 `RELIVE_NAS_HOST` 正确；开发机 SSH 密钥已加入 NAS；`ssh <host> true` 能非交互成功。工具用 `BatchMode=yes`，密钥需无口令或已加载到 agent。 |
| 另一备份正在运行（锁存在） | `<backup-root>/.relive-backup.lock` 存在。等待进行中的备份结束；工具绝不自动删除他人锁。确认无残留后手动清理。 |
| 磁盘空间不足 | `df` 检查备份根可用空间，需 ≥ 活动库大小 + `max(20% 库大小, 256MiB)`。清理旧备份或换盘。 |
| `quick_check` 非 ok | 活动库可能损坏。先在 NAS 上对活动库执行 `PRAGMA quick_check` 排查；备份对**备份**执行校验，失败会清理 `.partial` 并保留既有成功备份。 |
| 校验和失败 | 备份包写入异常（磁盘/网络/内存）。重新运行；若持续失败，检查 NAS 存储健康。失败会阻止最终改名，不会产生看似成功但损坏的目录。 |

---

## 只读预检（不创建备份）

如需在创建真实备份前确认 NAS 端依赖与路径就绪，可手动 SSH 只读检查：

```bash
ssh <RELIVE_NAS_HOST> 'command -v sqlite3 git docker tar sha256sum; \
  ls -d <RELIVE_NAS_ROOT> <RELIVE_NAS_DB> $(dirname <RELIVE_NAS_BACKUP_DIR>); \
  df -kP <RELIVE_NAS_BACKUP_DIR>'
```

在用户明确授权创建真实备份前，**不要**调用 `make backup-nas`。
