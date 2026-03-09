# Relive - 智能照片记忆相框

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Status](https://img.shields.io/badge/Status-Usable-brightgreen)]()
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)]()
[![Vue](https://img.shields.io/badge/Vue-3.5+-4FC08D?logo=vue.js)]()
[![Version](https://img.shields.io/badge/Version-1.0.0-blue)]()

> 通过 AI 分析 NAS 中的照片，在相框设备上以“往年今日”为核心展示回忆；未命中时自动回溯到最接近当天的历史记忆。

Relive 是一个面向 NAS / 自部署场景的照片分析与展示系统。

它由三部分组成：
- Web 管理后台：扫描、筛选、分析、配置、查看结果
- 后端服务：管理照片元数据、任务、设备和展示策略
- `relive-analyzer`：在另一台 AI 主机上批量分析照片的 API 模式工具

---

## 为什么用 Relive

- **把照片从“存着”变成“会被重新看到”**：围绕“往年今日”自动选图，不只是相册归档
- **支持多种 AI 提供者**：可接本地模型、远程 GPU、云端 API
- **适合 NAS 与 AI 主机分离场景**：通过 `relive-analyzer` API 模式批量分析，无需 `export.db` 文件交换
- **有完整管理后台**：扫描路径、AI 配置、设备管理、展示策略都可视化完成
- **为设备展示而设计**：不仅能分析照片，还能输出相框/设备侧可直接消费的展示结果

## 适合谁

**适合：**
- 有大量家庭照片，想在日常生活里重新看到回忆的人
- 用 NAS / Docker / 自部署管理照片的人
- 想把照片分析与设备展示串起来的人
- AI 服务和照片库不在同一台机器上的用户

**不太适合：**
- 只想用纯云端 SaaS 托管服务的人
- 不需要设备展示、只想找一个普通图库管理器的人
- 不愿意维护 Docker / 自部署环境的人

## 当前状态

- ✅ Web 管理后台可用
- ✅ 后端 API 与任务系统可用
- ✅ `relive-analyzer` API 模式可用
- ✅ 当前推荐批量分析工作流是 **API 模式**
- 📋 设备固件 / 更多硬件平台仍在后续阶段

如果你只关心“现在能不能用”，答案是：**可以先从 Web + Docker 跑起来，再按需要接入 analyzer 或设备端。**

---

## 快速开始

最短路径如下：

### 1. 克隆仓库

```bash
git clone https://github.com/davidhoo/relive.git
cd relive
```

### 2. 准备环境变量

```bash
cp .env.example .env
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

### 往年今日选图
- 优先展示历史同日附近的照片
- 统一兜底链路：严格往年今日 → 智能日期回溯 → 全局高分兜底
- 支持阈值过滤与去重策略

### 设备展示
- 设备由后台统一管理并分配 `api_key`
- 后端提供展示接口、预览接口和渲染资源
- 当前适合作为相框 / 嵌入式展示端的后端基础设施

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
- `docs/DEVELOPMENT.md`：开发说明
- `docs/ARCHITECTURE.md`：系统架构
- `docs/DEVICE_PROTOCOL.md`：设备协议设计
- `CHANGELOG.md`：最近变更记录

### 历史文档
- `docs/INDEX.md`：完整文档导航
- `docs/ANALYZER.md`：旧版文件模式说明（历史）
- `docs/OFFLINE_WORKFLOW.md`：早期离线工作流设计（历史）
- `docs/API_DESIGN.md`：设计阶段 API 方案

---

## Roadmap

- 继续完善设备固件 / 设备端接入体验
- 支持更多展示硬件平台
- 优化照片筛选、分析与展示体验
- 持续收紧文档与实现的一致性

## License

MIT
