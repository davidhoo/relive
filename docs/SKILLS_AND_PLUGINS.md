# Relive 项目 - Claude Code Skills & Plugins 推荐

> 基于 Golang + Docker + Web 技术栈的最佳实践
> 更新时间：2026-02-28

---

## 一、强烈推荐安装的官方插件

### 1. **gopls-lsp** ⭐⭐⭐⭐⭐
**用途**：Go 语言代码智能提示和分析

**为什么需要**：
- ✅ 自动补全 Go 代码
- ✅ 实时语法检查
- ✅ 代码重构支持
- ✅ 跳转到定义

**安装方法**：
```bash
# 1. 安装 gopls
go install golang.org/x/tools/gopls@latest

# 2. 确保 $GOPATH/bin 在 PATH 中
export PATH=$PATH:$(go env GOPATH)/bin

# 3. 验证安装
gopls version
```

**使用**：自动生效，Claude Code 会自动调用 gopls 理解你的 Go 代码

---

### 2. **feature-dev** ⭐⭐⭐⭐⭐
**用途**：系统化的功能开发工作流（7 阶段方法）

**为什么需要**：
- ✅ 自动探索代码库（code-explorer agent）
- ✅ 设计多种架构方案（code-architect agent）
- ✅ 代码质量审查（code-reviewer agent）
- ✅ 避免盲目开发，先理解再动手

**适用场景**：
- 开发新功能模块（如照片扫描服务、AI 分析服务）
- 需要架构设计决策时
- 不确定如何实现时

**工作流程**：
```
/feature-dev 实现照片扫描服务

Phase 1: 发现需求
Phase 2: 探索代码库（3个探索 agent 并行）
Phase 3: 提出问题
Phase 4: 设计架构（3个设计方案供选择）
Phase 5: 实现代码
Phase 6: 代码审查（3个审查 agent 并行）
Phase 7: 总结
```

**安装方法**：
```bash
claude plugin install feature-dev
```

---

### 3. **code-review** ⭐⭐⭐⭐
**用途**：PR 自动化代码审查

**为什么需要**：
- ✅ 4 个独立 agent 从不同角度审查
- ✅ 置信度评分（过滤误报）
- ✅ 自动检查项目规范（CLAUDE.md）
- ✅ 发现 bug 和质量问题

**适用场景**：
- 提交 PR 前自动审查
- 合并代码前质量检查

**使用**：
```bash
/code-review
```

**安装方法**：
```bash
claude plugin install code-review
```

---

### 4. **hookify** ⭐⭐⭐⭐
**用途**：创建自定义行为规则和防护

**为什么需要**：
- ✅ 防止危险操作（如 rm -rf）
- ✅ 确保代码规范（如禁止 console.log）
- ✅ 敏感文件保护（.env、密钥等）
- ✅ 提交前检查（如强制运行测试）

**推荐规则**：
```bash
# 1. 防止误删除
/hookify 警告我使用 rm -rf 命令时

# 2. 保护敏感配置
/hookify 编辑 .env 或包含 API_KEY 的文件时警告

# 3. Docker 操作提醒
/hookify 在执行 docker-compose down -v 时确认

# 4. 提交前检查（可选）
/hookify 提交代码前必须先运行测试
```

**安装方法**：
```bash
claude plugin install hookify
```

---

### 5. **frontend-design** ⭐⭐⭐
**用途**：生成高质量前端界面

**为什么需要**：
- ✅ 你的项目需要 Web 管理界面
- ✅ 自动生成美观、现代的 UI
- ✅ 避免"AI 生成的通用设计"
- ✅ 响应式设计

**适用场景**：
- 开发 Web 管理界面时
- 设计照片展示页面
- 配置界面设计

**使用**：
```
创建一个照片管理后台界面，包含：
- 照片列表和缩略图
- 筛选和搜索
- 批量操作
- 分析状态显示
```

**安装方法**：
```bash
claude plugin install frontend-design
```

---

### 6. **commit-commands** ⭐⭐⭐
**用途**：规范化 Git 提交

**为什么需要**：
- ✅ 自动生成规范的 commit message
- ✅ 遵循约定式提交（Conventional Commits）
- ✅ 提高项目可维护性

**安装方法**：
```bash
claude plugin install commit-commands
```

---

## 二、其他有用的插件

### 7. **pr-review-toolkit**
**用途**：PR 审查工具集
**适用场景**：如果你计划开源 Relive 项目

### 8. **security-guidance**
**用途**：安全性指导
**适用场景**：处理敏感数据（照片、API Key）

### 9. **claude-code-setup**
**用途**：自动推荐适合的插件和配置
**使用**：
```bash
/claude-automation-recommender
```

---

## 三、内置 Skill（已可用）

### `/simplify` ⭐⭐⭐⭐⭐
**用途**：代码质量审查和重构

**自动检查**：
- 代码重复（DRY 原则）
- 过度设计
- 性能问题
- 可读性改进

**使用场景**：
- 每个功能模块完成后运行
- 代码合并前质量检查

**使用**：
```bash
/simplify
```

**集成到工作流**：
```
1. 实现功能
2. 运行 /simplify 检查
3. 修复问题
4. 提交代码
```

---

## 四、推荐的插件安装顺序

### 第一批（立即安装）：
```bash
# 1. Go 语言支持（必需）
go install golang.org/x/tools/gopls@latest

# 2. 安装核心开发插件
claude plugin install feature-dev    # 功能开发工作流
claude plugin install hookify        # 行为规则保护
```

### 第二批（开始编码后）：
```bash
claude plugin install code-review      # 代码审查
claude plugin install frontend-design  # Web 界面开发
claude plugin install commit-commands  # 提交规范化
```

---

## 五、针对 Relive 项目的 Skill 使用建议

### 阶段 1：需求和设计阶段
**当前阶段** ✅
- 无需 Skill
- 专注于文档驱动开发
- 完善 REQUIREMENTS.md

### 阶段 2：架构设计和技术选型
**使用**：
- `/feature-dev` - 探索类似项目的架构模式
- 手动设计 + AI 辅助

### 阶段 3：核心服务开发

#### 3.1 照片扫描服务
```bash
/feature-dev 实现 NAS 照片扫描服务，支持增量扫描和 EXIF 提取
```
- Phase 2 会探索 Go 中的图片处理库
- Phase 4 会给出多种实现方案
- Phase 6 自动代码审查

完成后：
```bash
/simplify  # 优化代码质量
```

#### 3.2 AI 分析服务
```bash
/feature-dev 实现调用阿里 Qwen-VL API 的照片分析服务
```

#### 3.3 评分系统
```bash
/feature-dev 实现照片艺术性和回忆价值评分算法
```

#### 3.4 Web 管理界面
```bash
创建 Relive 的 Web 管理后台，包含：
- 照片列表和预览
- 分析状态监控
- 配置管理
- 手动触发全量分析
```
（自动触发 frontend-design）

### 阶段 4：集成和测试
```bash
/code-review     # 审查整体代码质量
/simplify        # 最终优化
```

---

## 六、Hookify 规则建议（针对 Relive）

### 创建项目专属保护规则：

#### 1. 防止误删照片数据库
```bash
/hookify 删除或清空数据库文件时警告
```

#### 2. 保护配置文件
```bash
/hookify 编辑包含 API_KEY 或 QWEN_API_KEY 的文件时警告
```

#### 3. Docker 安全
```bash
/hookify 执行 docker-compose down 带 -v 参数时确认（避免删除数据卷）
```

#### 4. Git 提交检查
```bash
/hookify 提交前检查 .env 文件是否在 .gitignore 中
```

---

## 七、开发工作流推荐

### 标准功能开发流程：
```
1. 更新需求文档
   ├─ 明确功能需求
   └─ 更新 REQUIREMENTS.md

2. 使用 /feature-dev 开发
   ├─ Phase 1-4: 探索和设计
   ├─ Phase 5: 实现代码
   └─ Phase 6: 代码审查

3. 使用 /simplify 优化
   ├─ 检查代码质量
   └─ 重构改进

4. 提交代码
   ├─ 自动生成规范 commit message
   └─ 更新文档（如有变化）
```

### PR 审查流程（如果多人协作）：
```
1. 创建 PR
2. 运行 /code-review
3. 修复问题
4. 合并
```

---

## 八、安装命令速查

```bash
# 查看可用插件
claude plugin list

# 安装插件
claude plugin install <plugin-name>

# 卸载插件
claude plugin uninstall <plugin-name>

# 查看已安装插件
claude plugin list --installed

# 安装推荐的插件组合（复制粘贴执行）
go install golang.org/x/tools/gopls@latest && \
claude plugin install feature-dev && \
claude plugin install hookify && \
claude plugin install code-review && \
claude plugin install frontend-design && \
claude plugin install commit-commands
```

---

## 九、性能和成本考虑

### Agent 和 Skill 的成本
- ⚠️ feature-dev 会启动多个 agent（6-9 个），消耗较多 token
- ⚠️ code-review 会启动 4 个 agent
- 💡 建议：仅在关键功能开发时使用，小修小补直接写代码

### 优化建议
- ✅ 明确需求后再用 /feature-dev（避免频繁探索）
- ✅ 小功能直接实现 + /simplify
- ✅ 大功能使用完整 /feature-dev 流程

---

## 十、常见问题

### Q: 安装插件后不生效？
A: 重启 Claude Code 会话或使用 `claude --help` 验证

### Q: gopls 找不到？
A: 确保 `$GOPATH/bin` 在 PATH 中：
```bash
echo $GOPATH/bin
# 如果为空，运行：
export PATH=$PATH:$(go env GOPATH)/bin
```

### Q: feature-dev 太慢？
A: 正常现象，多个 agent 并行运行需要时间，但换来的是高质量设计

### Q: hookify 规则不生效？
A: 检查 `.claude/hookify.*.local.md` 文件是否在项目根目录

---

## 总结

**必装（5 个）**：
1. ✅ gopls-lsp - Go 语言支持
2. ✅ feature-dev - 系统化开发
3. ✅ hookify - 安全保护
4. ✅ frontend-design - Web 界面
5. ✅ 内置 /simplify - 代码优化

**推荐工作流**：
```
需求文档 → /feature-dev → /simplify → 提交
```

这套工具链将大幅提升 Relive 项目的开发质量和效率！
