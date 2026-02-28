# Relive 开发速查卡

## 🚀 常用 Skills 快速参考

### 功能开发
```bash
/feature-dev              # 大功能：7阶段完整流程
/simplify                 # 优化代码（每次必用）
```

### Git 操作
```bash
/commit                   # 智能提交
/commit-push-pr          # 提交+推送+创建PR
/clean_gone              # 清理已合并分支
```

### 代码审查
```bash
/code-review             # PR 自动审查（4 agents）
```

### 安全规则
```bash
/hookify                 # 创建保护规则
/hookify:list           # 查看规则
/hookify:configure      # 配置规则
```

---

## 🛡️ 已启用的保护规则

| 规则 | 触发条件 | 动作 |
|------|---------|------|
| env-protection | 编辑 .env 文件 | ⚠️ 警告 |
| dangerous-rm | rm -rf 危险路径 | 🛑 阻止 |
| docker-volume | docker-compose down -v | ⚠️ 警告 |
| hardcoded-secrets | 代码中硬编码密钥 | ⚠️ 警告 |

---

## 📝 Relive 开发流程

### 新功能
```
/feature-dev → /simplify → /commit
```

### 小修改
```
(写代码) → /simplify → /commit
```

### PR 提交
```
/code-review → (修复) → /commit-push-pr
```

---

## 🔧 工具验证

### 检查 Go
```bash
go version              # Go 1.24.5 ✅
```

### 检查 gopls
```bash
$(go env GOPATH)/bin/gopls version
# golang.org/x/tools/gopls v0.21.1 ✅
```

### 检查插件
```bash
claude plugin list
# feature-dev ✅
# hookify ✅
# frontend-design ✅
# code-review ✅
# commit-commands ✅
```

---

## 💡 Relive 技术栈

- **语言**: Golang
- **AI**: 阿里通义千问 Qwen-VL
- **部署**: Docker on 群晖 NAS
- **前端**: Web 管理界面
- **硬件**: ESP32 + 墨水屏

---

## 📚 文档索引

- `REQUIREMENTS.md` - 需求文档 📋
- `METHODOLOGY.md` - 开发方法论 📖
- `SKILLS_CATALOG.md` - Skills 清单 📚
- `ARCHITECTURE.md` - 架构设计 🏗️
- `INSTALLATION_SUMMARY.md` - 安装总结 ✅

---

## 🎯 当前状态

- ✅ 项目结构已创建
- ✅ 开发环境已配置
- ✅ Skills 全部安装
- ✅ 保护规则已生效
- 🔄 需求文档完善中（下一步）

---

**快速帮助**: 输入 `/hookify:help` 查看规则帮助
