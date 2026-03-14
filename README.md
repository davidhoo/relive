# Relive - 让照片重新活过来

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Status](https://img.shields.io/badge/Status-Usable-brightgreen)]()
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)]()
[![Vue](https://img.shields.io/badge/Vue-3.5+-4FC08D?logo=vue.js)]()
[![Version](https://img.shields.io/badge/Version-1.0.2-blue)]()

> 你的 NAS 里存了多少照片？它们上一次被翻看是什么时候？
> Relive 通过 AI 理解每张照片，以”往年今日”为线索，每天把值得重温的记忆送到你的相框上。

<img src="docs/images/photo-frame.png" width="400" alt="ESP32 E-Ink Photo Frame">

Relive 是一个自部署的照片回忆系统 —— 扫描你 NAS 中的照片，用 AI 理解内容，然后每天在相框或屏幕上呈现值得重温的瞬间。

它由四部分组成：
- **Web 管理后台**：扫描照片、配置 AI、管理设备和展示策略
- **后端服务**：处理照片分析、地理编码、缩略图生成等后台任务
- **relive-analyzer**：独立的批量分析工具，适合在另一台 AI 主机上运行
- **展示终端**：目前已支持 ESP32 墨水屏相框，未来可扩展到电脑屏保、移动端 App、微信小程序等

---

## 为什么用 Relive

- **让照片不再只是”存着”**：围绕”往年今日”自动选图，每天重温不同年份的这一天
- **AI 真正理解照片内容**：不只是看 EXIF，而是理解场景、人物、氛围，给出评分和描述
- **支持多种 AI 部署方式**：本地模型、远程 GPU、云端 API，按需选择
- **NAS 与 AI 主机可以分开部署**：通过 `relive-analyzer` 在另一台机器上批量分析
- **完整的可视化管理**：扫描路径、AI 配置、设备管理、展示策略，全部在 Web 后台完成

## 适合谁

- 有大量家庭照片，想在日常生活中重新看到它们的人
- 用 NAS / Docker 自部署管理照片的人
- 想把照片分析和相框展示串起来的人

> Relive 需要 Docker 自部署环境，目前不提供云端托管服务。

## 当前状态

- ✅ Web 管理后台可用
- ✅ 后端 API 与任务系统可用
- ✅ `relive-analyzer` API 模式可用
- ✅ 当前推荐批量分析工作流是 **API 模式**
- ✅ ESP32 墨水屏相框固件（AP 配网、定时睡眠、双配置源）

如果你只关心”现在能不能用”，答案是：**可以先从 Web + Docker 跑起来，再按需要接入 analyzer 或设备端。**

## 系统截图

<details>
<summary>点击展开截图</summary>

### 仪表盘
![Dashboard](docs/images/dashboard.png)

### 展示策略 - 往年今日选图
![Display Strategy](docs/images/display-strategy.png)

### 照片管理
![Photos](docs/images/photos.png)

### AI 分析
![AI Analysis](docs/images/ai-analysis.png)

### 设备管理
![Devices](docs/images/devices.png)

### 缩略图生成
![Thumbnails](docs/images/thumbnails.png)

### GPS 位置解析
![Geocoding](docs/images/geocoding.png)

</details>

---

## 快速开始

最短路径如下：

### 1. 克隆仓库

```bash
git clone https://github.com/davidhoo/relive.git
cd relive
```

### 2. 准备环境变量和生产配置

```bash
cp .env.example .env
cp backend/config.prod.yaml.example backend/config.prod.yaml
```

建议至少修改：

```env
JWT_SECRET=replace-with-a-random-secret
```

### 3. 配置照片目录挂载

编辑 `docker-compose.yml`，把宿主机照片目录挂到容器内：

```yaml
services:
  relive:
    volumes:
      - /your/photo/library:/app/photos:ro
```

### 4. 启动服务

```bash
make deploy
```

### 5. 首次初始化

访问 `http://localhost:8080`，然后按这个顺序操作：
1. 使用默认账号 `admin / admin` 登录并修改密码
2. 到“配置管理”添加扫描路径，例如 `/app/photos`
3. 到“照片管理”执行扫描或重建
4. 如需 AI 分析，在“配置管理”中设置 AI Provider
5. 到“AI 分析”页面启动在线分析，或使用下方的 analyzer API 模式

更详细的部署说明：
- `QUICKSTART.md`
- `docs/QUICKSTART.md`
- `docs/CONFIGURATION.md`（配置职责与优先级）

---

## 使用方式

### 方式 1：直接在 Web 中扫描 + 在线分析

**适合：**
- 照片量不大
- AI 服务与 Relive 服务在同一网络内
- 你想直接在后台里完成全部操作

**基本流程：**
1. 添加扫描路径
2. 扫描照片
3. 配置 AI Provider
4. 在“AI 分析”页面启动分析
5. 在“照片管理”与“展示策略”中查看和使用结果

### 方式 2：使用 `relive-analyzer` API 模式批量分析

**适合：**
- 照片量大
- AI 主机与 NAS / 主服务分离
- 想把分析任务放到另一台更强的机器上执行

**基本流程：**
1. 在“设备管理”里创建 `offline` 或 `service` 类型设备
2. 复制生成的 `api_key`
3. `cp analyzer.yaml.example analyzer.yaml`
4. 填写 `server.endpoint` 与 `server.api_key`
5. 构建并运行 analyzer：

```bash
make build-analyzer
./backend/bin/relive-analyzer check -config analyzer.yaml
./backend/bin/relive-analyzer analyze -config analyzer.yaml
```

详细说明：`docs/ANALYZER_API_MODE.md`

---

## 核心能力

### AI 照片分析
- 理解照片内容、人物、场景和氛围
- 生成描述、短句、分类、标签与评分
- 支持 Ollama / vLLM / Qwen / OpenAI / Hybrid

### 往年今日
- 每天自动挑选历史上同一天或相近日期的照片
- 没有往年今日时，智能回溯到最近的历史记忆，确保每天都有内容
- 支持评分过滤，只展示值得回忆的照片

### 设备展示
- 支持 ESP32 墨水屏相框、Web 浏览器等多种展示终端
- 后台统一管理设备，自动推送当日展示内容
- 墨水屏相框支持 AP 配网、定时睡眠等低功耗特性

### Web 管理后台
- 浏览照片和分析结果
- 管理扫描路径、提示词、AI Provider、设备和展示策略
- 查看缩略图、地理编码、分析等后台任务状态

### 图片预处理与成本优化
- 分析前自动压缩图片
- 减少带宽与 API 成本
- 适合大规模照片库的批量处理场景

---

## 文档导航

### 当前使用
- `QUICKSTART.md`：仓库根目录快速启动
- `docs/QUICKSTART.md`：NAS / 服务器部署入口
- `docs/BACKEND_API.md`：当前已实现 API
- `docs/ANALYZER_API_MODE.md`：当前 analyzer API 模式
- `docs/PROJECT_STATUS.md`：当前项目状态

### 当前开发
- `docs/QUICK_REFERENCE.md`：开发速查卡
- `docs/CONFIGURATION.md`：配置职责与优先级
- `docs/DEVICE_PROTOCOL.md`：设备协议设计
- `CHANGELOG.md`：最近变更记录

### 历史文档
- `docs/INDEX.md`：完整文档导航
- `docs/ANALYZER.md`：旧版文件模式说明（历史）
- `docs/OFFLINE_WORKFLOW.md`：早期离线工作流设计（历史）
- `docs/API_DESIGN.md`：设计阶段 API 方案
- `docs/archive/ARCHITECTURE.md`：系统架构（设计阶段）
- `docs/archive/DEVELOPMENT.md`：开发说明（设计阶段）
- `docs/archive/DEPLOYMENT.md`：部署指南（设计阶段）
- `docs/archive/AI_PROVIDERS.md`：AI Provider 架构（设计阶段）
- `docs/archive/ANALYZER_PHASE1_DONE.md`：Analyzer Phase 1 完成总结
- `docs/archive/ANALYZER_TEST_REPORT.md`：Analyzer 测试报告

---

## Roadmap

- 支持更多展示终端（Android、iOS）
- 照片过滤与排除系统

## 致谢

- [InkTime](https://github.com/dai-hongtao/InkTime) - 墨水屏相框的灵感来源，我们学习和参照了他的想法

## License

MIT
