# 分析模式入门文档消歧 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**状态：** Completed
**Goal：** 让首次部署 Relive 的用户能在进入具体配置前，准确选出“Web 直接分析”或“外部 `relive-analyzer` API 模式”，并明确后者不是断网的文件导入导出流程。

**Architecture：** 本任务只调整当前使用文档与配置模板中的说明，不改后端路由、CLI、任务领取逻辑、设备类型或 AI Provider 配置。`README.md` 和 `QUICKSTART.md` 提供相同的选择入口；`docs/ANALYZER_API_MODE.md` 作为 API 模式的唯一详细解释，说明主服务与外部 worker 的职责、连通性和历史文件模式边界。

**Tech Stack：** Markdown、当前 `relive-analyzer` CLI、HTTP API、`analyzer.yaml.example`。

---

## 背景与问题

现有文档已经分别描述了两种路径，但用户需要在三个文件间自行推断以下事实：

- “在线分析”是从 Web 管理后台启动，AI Provider 必须能被 Relive 服务访问；
- `relive-analyzer` 适合把分析负载放到另一台较强的机器上，NAS / Relive 服务继续持有照片库与任务状态；
- `offline` 是设备类型，不表示 analyzer 可在与 Relive 服务完全断网的环境中运行；当前 analyzer 通过 HTTP 领取任务、下载图片并提交结果；
- 当前模式不是历史的 `export.db` 导出 → 本地分析 → 导入结果工作流。

## 统一术语与读者决策

后续文案必须使用下列名称；代码枚举和现有 API 名称保持不变：

| 面向用户的名称 | 指什么 | 适合谁 | 必须满足 |
| --- | --- | --- | --- |
| Web 直接分析（现有 UI 名称：在线分析） | 在 Relive Web 管理后台发起的内置分析路径 | 照片量较小，或 AI Provider 与 Relive 服务网络可达 | Relive 服务可访问已配置的 AI Provider |
| 外部 `relive-analyzer`（API 模式） | 在另一台机器运行的 CLI worker | 照片量大，或需要把 AI 推理移到 Mac / GPU 主机 | worker 可通过 HTTP 访问 Relive 服务，且持有设备 API Key |

禁止把 API 模式表述为“完全离线”“断网分析”或当前推荐的“导出/导入模式”。需要提到历史方式时，明确标为历史文件模式，并链接 `docs/ANALYZER.md`。

## Task 1：在两个首次阅读入口放入相同的模式选择说明

**Files:**
- Modify: `README.md`（“首次初始化”和“使用方式”）
- Modify: `QUICKSTART.md`（第 5 步初始化说明与第 6 节前）
- Test: 文档阅读断言（无新增自动化测试）

**Step 1: 写出失败的读者断言**

在修改前，按下列问题逐项阅读 `README.md` 和 `QUICKSTART.md`。当前任一入口若不能在一次阅读中回答，即视为断言失败：

1. 我是否必须安装 `relive-analyzer` 才能开始使用 Relive？
2. 哪一种方式适合“NAS 负责照片库、Mac / GPU 主机负责推理”？
3. API 模式中的“offline”能否在无法访问 Relive 服务时运行？
4. 是否还应使用 `export.db` 导出/导入？

**Step 2: 将失败断言固化为共享的决策表**

在两个入口都放置同一张紧凑的两行表，位于用户第一次看到“AI 分析”之前。表至少包含“方式”“何时选择”“是否需要单独安装”“网络要求”四列，并表达：

- Web 直接分析：不安装 analyzer；由 Web 后台启动；Relive 服务必须能访问 Provider。
- 外部 `relive-analyzer` API 模式：需在外部主机安装 / 运行 CLI；该主机必须通过 HTTP 访问 Relive 服务；不是断网文件模式。

表后给出明确选择句：不确定时先走 Web 直接分析；只有需要把推理移到另一台机器或批量处理较大照片库时，再按 API 模式章节接入 analyzer。

**Step 3: 只保留一个详细入口**

将 `README.md` 的 API 模式流程缩短为“选择理由 + 最小 5 步 + 指向详细文档”，避免与 `QUICKSTART.md` 复制出会漂移的参数说明。两个入口均链接 `docs/ANALYZER_API_MODE.md`，且保留现有的 `analyzer.yaml.example` 命令作为可复制起点。

**Step 4: 运行文档断言**

Run:

```bash
rg -n -i 'Web 直接分析|在线分析|API 模式|断网|export\.db|外部.*analyzer' README.md QUICKSTART.md
```

Expected: 两个文件均出现两种模式、HTTP 连通性和“不使用 `export.db`”的明确说明；没有将 API 模式称作完全离线的文案。

**Step 5: Commit**

```bash
git add README.md QUICKSTART.md
git commit -m "docs: clarify analysis mode selection"
```

## Task 2：把 API 模式的运行拓扑和边界写成单一真值

**Files:**
- Modify: `docs/ANALYZER_API_MODE.md`（概述、适用场景、不适用场景）
- Modify: `analyzer.yaml.example`（文件头注释）
- Test: 文档与现有 API / CLI 的一致性检查（无新增自动化测试）

**Step 1: 写出失败的一致性断言**

在修改前，以 `docs/ANALYZER_API_MODE.md` 中的 API 表和 `analyzer.yaml.example` 为准，确认读者可据此回答：

- 哪一方保存任务真值并分配工作；
- analyzer 是否需要 `server.endpoint` 和 `server.api_key`；
- analyzer 为何能在另一台机器执行，但不能在与 Relive 服务隔绝的网络中执行；
- 历史文件交换流程现在应到哪里查看。

若上述任一点未在文档开头的概览区出现，断言失败。

**Step 2: 在概述后加入“API 模式不是断网离线”说明**

用简短职责流描述当前实现，不新增未实现能力：

```text
Relive 主服务（照片库、任务分配、结果持久化）
        ⇅ HTTP + Device API Key
外部 relive-analyzer（下载照片、调用 AI Provider、提交结果）
```

紧接着明确：外部 worker 可以运行在 NAS 以外的 Mac / GPU 主机；“offline”仅是设备类别 / 外部 worker 的部署位置，不代表可脱离 Relive 服务运行。旧 `export.db` 文件模式不是当前流程，并指向 `docs/ANALYZER.md` 获取历史背景。

**Step 3: 收敛适用与不适用场景的语言**

保留现有“NAS 与 AI 主机分离”“多台分析主机并发”的场景；补充“worker 必须可访问 `server.endpoint`”的前置条件。保留“完全无法访问 Relive 服务”和“希望沿用 `export.db` 文件交换”均不适用，避免新增对网络拓扑、远程存储或同步频率的推测。

**Step 4: 更新配置模板的第一屏提示**

将 `analyzer.yaml.example` 的文件头从泛称“离线分析工具”补为“外部 API 分析 worker”。在 `server.endpoint` 注释旁明确：填写 analyzer 所在主机可访问的 Relive 地址；不要将 endpoint 写成仅 NAS 容器内部可解析、外部主机无法访问的地址。

不得改动任何 YAML 键、默认值、环境变量名或 Provider 配置。

**Step 5: 运行一致性检查**

Run:

```bash
rg -n 'server\.endpoint|server\.api_key|/api/v1/analyzer/(tasks|results)|export\.db|HTTP' docs/ANALYZER_API_MODE.md analyzer.yaml.example
git diff --check
```

Expected: 文档仍引用现存配置键和路由，且 `git diff --check` 无空白错误。

**Step 6: Commit**

```bash
git add docs/ANALYZER_API_MODE.md analyzer.yaml.example
git commit -m "docs: explain analyzer API mode topology"
```

## Task 3：做跨入口一致性复核并更新索引状态

**Files:**
- Modify: `docs/INDEX.md`（将本计划转为 Completed）
- Verify: `README.md`
- Verify: `QUICKSTART.md`
- Verify: `docs/ANALYZER_API_MODE.md`
- Verify: `analyzer.yaml.example`
- Test: 文档交叉检查

**Step 1: 执行读者路径检查**

按三条路径通读，而不是只检查关键字：

1. 只有 NAS 和少量照片：确认读者能选 Web 直接分析，且不会误以为必须部署 CLI。
2. NAS 存照片、Mac / GPU 主机做推理：确认读者能选 API 模式，并看到 API Key 和 HTTP 连通性前置条件。
3. 期望断网或 `export.db` 文件交换：确认读者会被明确导向“不适用”与历史文档，而不会开始错误配置。

**Step 2: 校验所有可复制命令和链接**

Run:

```bash
rg -n 'make build-analyzer|relive-analyzer (check|analyze).*analyzer\.yaml|docs/ANALYZER_API_MODE\.md|docs/ANALYZER\.md' README.md QUICKSTART.md docs/ANALYZER_API_MODE.md docs/INDEX.md
git diff --check
```

Expected: 构建、连通性检查和启动命令仍与当前 CLI 一致；每个当前使用入口链接 API 模式说明，历史链接只指向 `docs/ANALYZER.md`。

**Step 3: 更新计划状态**

将本文件从 `Pending` 改为 `Completed`，并在 `docs/INDEX.md` 的 `Plan Status` 中将同一条目从 `Pending` 移到 `Completed`。只在全部交叉检查通过后执行此步。

**Step 4: Commit**

```bash
git add docs/INDEX.md docs/plans/2026-08-14-analyzer-mode-onboarding-documentation.md
git commit -m "docs: complete analyzer onboarding clarification"
```

## 验收标准

- 新用户在 `README.md` 或 `QUICKSTART.md` 的同一位置即可选择两种分析方式，无需先理解历史文档。
- API 模式被准确描述为“外部 HTTP worker”，不再让人将其理解成断网或文件导入导出工作流。
- 文档明确 Web 直接分析不依赖 `relive-analyzer`，API 模式明确要求外部机器可访问 Relive 和有效 Device API Key。
- 所有命令、YAML 键和路由描述继续与 `analyzer.yaml.example`、`backend/cmd/relive-analyzer/main.go`、`backend/internal/api/v1/router/router.go` 一致。
- `git diff --check` 通过；不要求改动 Go、Vue、SQLite schema 或运行中的 NAS 配置。

## 回滚

本任务只涉及 Markdown 与配置模板注释。若新术语引发歧义，回退本任务的三个文档提交即可；无需迁移数据、重启服务或改动用户生成的 `analyzer.yaml`。

## 不包含

- 不重命名 `offline` / `service` 设备类型，也不改变 API Key 认证方式。
- 不恢复或实现 `export.db` 导出 / 导入工作流。
- 不改变线上分析、analyzer 任务领取、失败退避、运行时租约或 AI Provider 行为。
- 不调整 Docsbook、SEO、第三方站点收录或 GitHub 仓库授权。
