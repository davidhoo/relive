# Relive - 智能照片记忆相框

Relive 是一个智能照片管理和展示系统，通过 AI 分析 NAS 中的照片，自动挑选最值得回忆的时刻，在墨水屏相框上展示"往年今日"或最珍贵的回忆。

## 核心功能

- 🤖 **AI 照片分析**：使用 Qwen3-VL API 理解照片内容
- 📝 **智能描述生成**：为每张照片生成 80-200 字的详细描述和 8-30 字的精美文案
- 🎨 **多维度评分**：
  - 美观艺术性评分（0-100）
  - 值得回忆评分（0-100）
- 🏷️ **自动分类**：智能照片分类管理
- 📅 **往年今日**：自动展示历史上的今天
- 🖼️ **墨水屏展示**：ESP32 驱动的电子相框

## 项目结构

```
relive/
├── backend/              # Python 后端服务
│   ├── src/             # 源代码
│   │   ├── api/         # REST API 接口
│   │   ├── services/    # 业务逻辑（照片分析、评分）
│   │   ├── models/      # 数据模型
│   │   ├── utils/       # 工具函数
│   │   └── config/      # 配置管理
│   ├── requirements.txt # Python 依赖
│   └── main.py          # 应用入口
├── database/            # 数据库
│   ├── migrations/      # 数据库迁移
│   └── schemas/         # 数据库表结构
├── esp32/               # ESP32 固件（墨水屏控制）
├── docs/                # 文档
└── tests/               # 测试
```

## 技术栈

- **后端**: Python 3.11+ + FastAPI
- **数据库**: SQLite / PostgreSQL
- **AI**: Qwen3-VL API
- **硬件**: ESP32 + 墨水屏

## 快速开始

（待完善...）

## 开发路线

- [ ] Phase 1: 后端服务和照片分析
- [ ] Phase 2: 数据库设计和存储
- [ ] Phase 3: API 接口开发
- [ ] Phase 4: ESP32 固件开发
- [ ] Phase 5: 完整联调

## License

MIT
