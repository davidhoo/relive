# Relive - 智能照片记忆相框

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Status](https://img.shields.io/badge/Status-Requirements%20Phase-blue)]()
[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)]()

> 通过 AI 分析 NAS 中的照片，在墨水屏相框上智能展示"往年今日"或最值得回忆的时刻

Relive 是一个智能照片管理和展示系统，它能自动理解照片内容、生成优美文案、智能评分，并在墨水屏电子相框上展示最值得重温的记忆。

---

## ✨ 核心功能

### 🤖 AI 智能分析
- **内容理解**：使用阿里通义千问 Qwen-VL API 深度理解照片内容
- **详细描述**：为每张照片生成 80-200 字的客观描述
- **优美文案**：智能生成 8-30 字的精美短句，为画面补上"画外之意"
- **自动分类**：8大主分类 + 多种辅助标签（事件、情绪、季节）

### 🎨 双维度评分
- **回忆价值评分**（0-100）：评估照片的纪念意义和情感价值
- **美观度评分**（0-100）：客观评价构图、光线、色彩等摄影质量
- **智能算法**：综合评分 = 回忆价值×0.7 + 美观度×0.3

### 📅 往年今日
- **时光回溯**：自动展示"历史上的今天"拍摄的照片
- **智能降级**：±3天 → ±7天 → 本月 → 年度最佳
- **避免重复**：7天内不重复展示同一张照片

### 🖼️ 墨水屏展示
- **硬件支持**：ESP32 驱动，7.3寸彩色墨水屏（支持多种规格）
- **低功耗**：深度睡眠模式，2节18650电池可用约半年
- **灵活刷新**：定时自动 + 按钮手动，支持一天多次更新

### 🌐 Web 管理
- **可视化管理**：浏览所有照片和分析结果
- **配置界面**：扫描设置、展示策略、排除目录
- **进度监控**：实时查看扫描进度和成本统计
- **手动触发**：一键触发扫描或重新分析

---

## 🏗️ 技术架构

### 后端技术栈
- **语言**：Golang 1.21+
- **框架**：待定（Gin / Echo / Fiber）
- **数据库**：SQLite（可选 PostgreSQL）
- **AI 服务**：阿里云百炼平台 Qwen-VL API
- **部署**：Docker 容器化，运行在群晖 NAS

### 前端技术栈
- **Web 界面**：待定（React / Vue / Svelte）
- **UI 框架**：待定

### 硬件技术栈
- **主控**：ESP32-S3（需要 PSRAM ≥384KB）
- **显示**：7.3寸彩色墨水屏 GDEP073E01（可配置其他型号）
- **通信**：WiFi 2.4GHz + HTTP API
- **电源**：2×18650 锂电池（可选）

---

## 📁 项目结构

```
relive/
├── backend/              # Golang 后端服务
│   ├── cmd/             # 应用入口
│   ├── internal/        # 内部包
│   │   ├── api/         # REST API 接口
│   │   ├── service/     # 业务逻辑
│   │   ├── model/       # 数据模型
│   │   ├── scanner/     # 照片扫描
│   │   ├── analyzer/    # AI 分析
│   │   └── scorer/      # 评分算法
│   ├── pkg/             # 公共包
│   └── config/          # 配置文件
├── web/                 # Web 管理界面
├── esp32/               # ESP32 固件
├── database/            # 数据库
│   ├── migrations/      # 数据库迁移
│   └── schemas/         # 表结构定义
├── docs/                # 项目文档 📚
│   ├── REQUIREMENTS.md  # 需求文档 ✅
│   ├── ARCHITECTURE.md  # 架构设计
│   └── API_DESIGN.md    # API 设计
├── scripts/             # 辅助脚本
├── docker/              # Docker 配置
└── tests/               # 测试
```

---

## 📊 项目状态

### ✅ 已完成
- [x] 项目初始化和环境配置
- [x] 开发方法论制定
- [x] 需求文档编写（完整）
- [x] 参考 [InkTime](https://github.com/dai-hongtao/InkTime) 优秀实践

### 🔄 进行中
- [ ] 数据库详细设计
- [ ] API 接口设计
- [ ] 架构设计完善

### 📋 待开始
- [ ] Golang 后端开发
- [ ] Web 界面开发
- [ ] ESP32 固件开发
- [ ] Docker 部署配置
- [ ] 完整测试和优化

---

## 🚀 快速开始

> 项目目前处于需求和设计阶段，尚未开始编码。

### 开发计划

**Phase 1: 设计阶段**（当前）
```bash
1. 完善数据库设计（DATABASE_SCHEMA.md）
2. 完善 API 接口设计（API_DESIGN.md）
3. 完善架构设计（ARCHITECTURE.md）
```

**Phase 2: 后端开发**
```bash
1. 照片扫描服务
2. AI 分析服务（Qwen API 集成）
3. 评分算法实现
4. REST API 开发
```

**Phase 3: 前端开发**
```bash
1. Web 管理界面
2. 可视化展示
3. 配置管理
```

**Phase 4: 硬件开发**
```bash
1. ESP32 固件开发
2. 墨水屏驱动适配
3. 低功耗优化
```

**Phase 5: 集成测试**
```bash
1. 功能测试
2. 性能优化
3. 用户体验优化
```

---

## 📖 文档

### 核心文档
- [需求文档](docs/REQUIREMENTS.md) - 完整的功能需求和技术需求 ✅
- [需求总结](docs/REQUIREMENTS_SUMMARY.md) - 需求文档完成总结 ✅
- [架构设计](docs/ARCHITECTURE.md) - 系统架构设计（待完善）
- [开发方法论](docs/METHODOLOGY.md) - 文档驱动开发方法论
- [Skills 清单](docs/SKILLS_CATALOG.md) - Claude Code Skills 使用指南

### 设计文档（待创建）
- [ ] DATABASE_SCHEMA.md - 数据库详细设计
- [ ] API_DESIGN.md - API 接口规范
- [ ] ESP32_PROTOCOL.md - ESP32 通信协议
- [ ] DEPLOYMENT.md - 部署指南

---

## 🎨 设计亮点

### 参考优秀项目
本项目参考了优秀开源项目 [InkTime](https://github.com/dai-hongtao/InkTime)：
- ✅ 成熟的照片评分体系
- ✅ 经过验证的展示策略
- ✅ 墨水屏低功耗方案

### 创新优化
- ✅ **多次展示支持**：一天可请求多次，每次返回不同照片
- ✅ **多规格适配**：支持不同型号的墨水屏
- ✅ **灵活扫描**：限速模式（平衡成本）+ 全速模式（可选）
- ✅ **Golang 重写**：更高性能，更好的并发处理
- ✅ **Docker 部署**：简化部署，适配群晖 NAS

---

## 💰 成本估算

### API 调用成本（阿里云 Qwen-VL）
- **首次全量分析**：约 ¥2,200（11万张照片）
- **日常维护**：约 ¥5-10/月（仅新增照片）
- **分摊策略**：限速模式 3-6 个月完成，平摊成本

### 硬件成本
- **ESP32-S3 开发板**：约 ¥30-50
- **7.3寸彩色墨水屏**：约 ¥200-400
- **其他配件**：约 ¥50
- **总计**：约 ¥300-500

---

## 🔒 隐私和安全

- ✅ **数据本地化**：照片文件保存在 NAS，不上传云端
- ✅ **临时分析**：仅在分析时临时上传到 Qwen API
- ✅ **阿里云承诺**：不保存用户上传的图片
- ✅ **排除目录**：支持配置敏感目录排除列表
- ✅ **访问控制**：Web 界面需要身份认证
- ✅ **数据加密**：支持数据库加密存储

---

## 🤝 贡献指南

欢迎贡献代码、报告问题或提出建议！

### 贡献方式
1. Fork 本仓库
2. 创建特性分支（`git checkout -b feature/AmazingFeature`）
3. 提交更改（`git commit -m 'Add some AmazingFeature'`）
4. 推送到分支（`git push origin feature/AmazingFeature`）
5. 开启 Pull Request

### 开发规范
- 遵循文档驱动开发（DDD）
- 代码提交前运行 `/simplify` 优化
- 使用 `/commit` 生成规范的 commit message

---

## 📝 License

本项目采用 MIT 许可证 - 查看 [LICENSE](LICENSE) 文件了解详情。

### 第三方许可
- **GeoNames 城市数据**：CC BY 4.0
- **参考项目 InkTime**：MIT

---

## 🙏 致谢

- [InkTime](https://github.com/dai-hongtao/InkTime) - 优秀的墨水屏相框项目，提供了宝贵的设计参考
- [阿里云百炼平台](https://www.aliyun.com/product/bailian) - 提供 Qwen-VL API 服务
- [GeoNames](https://www.geonames.org/) - 提供城市地理数据

---

## 📞 联系方式

- **GitHub**: [@davidhoo](https://github.com/davidhoo)
- **项目地址**: https://github.com/davidhoo/relive
- **问题反馈**: [Issues](https://github.com/davidhoo/relive/issues)

---

## ⭐ Star History

如果这个项目对你有帮助，请给它一个 Star ⭐！

---

<p align="center">
  <strong>让每一张照片都重新"活"起来</strong><br>
  <em>Relive - 重温珍贵时刻</em>
</p>
