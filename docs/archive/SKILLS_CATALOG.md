# Claude Code - 可用 Skills 完整清单

> 基于官方插件市场
> 更新时间：2026-02-28

---

## 一、内置 Skills（无需安装）

### `/simplify` ⭐⭐⭐⭐⭐
**来源**：code-simplifier（内置 agent）
**用途**：代码质量审查和重构
**使用**：
```bash
/simplify
```

**功能**：
- ✅ 保持功能不变，优化代码结构
- ✅ 应用项目规范（CLAUDE.md）
- ✅ 减少复杂度和嵌套
- ✅ 消除冗余代码
- ✅ 提高可读性
- ✅ 避免过度简化
- ✅ 自动关注最近修改的代码

**适用场景**：
- 每个功能模块完成后
- 代码合并前质量检查
- 重构优化

**模型**：Opus（高质量）

---

## 二、功能开发类 Skills

### 1. `/feature-dev` ⭐⭐⭐⭐⭐
**来源**：feature-dev 插件
**用途**：系统化的 7 阶段功能开发工作流

**安装**：
```bash
claude plugin install feature-dev
```

**使用**：
```bash
/feature-dev 实现照片扫描服务
# 或直接
/feature-dev
```

**7 阶段流程**：
1. **Discovery** - 理解需求
2. **Codebase Exploration** - 探索代码（启动 2-3 个 code-explorer agents）
3. **Clarifying Questions** - 提出问题
4. **Architecture Design** - 设计架构（启动 2-3 个 code-architect agents）
5. **Implementation** - 实现代码
6. **Quality Review** - 质量审查（启动 3 个 code-reviewer agents）
7. **Summary** - 总结

**相关 Agents**：
- `code-explorer` - 深度代码探索
- `code-architect` - 架构设计
- `code-reviewer` - 代码审查

**适用场景**：
- 新功能开发
- 需要架构决策
- 复杂集成
- 需求不明确时

---

## 三、代码审查类 Skills

### 2. `/code-review` ⭐⭐⭐⭐
**来源**：code-review 插件
**用途**：PR 自动化代码审查

**安装**：
```bash
claude plugin install code-review
```

**使用**：
```bash
/code-review
```

**功能**：
- ✅ 4 个独立 agent 并行审查
- ✅ Agent #1-2: CLAUDE.md 合规性检查
- ✅ Agent #3: Bug 检测
- ✅ Agent #4: Git 历史上下文分析
- ✅ 置信度评分（0-100）
- ✅ 仅展示 ≥80 分的问题（过滤误报）

**适用场景**：
- PR 提交前
- 代码合并前
- 团队协作审查

### 3. `/review-pr` ⭐⭐⭐
**来源**：pr-review-toolkit 插件
**用途**：PR 审查工具集

**安装**：
```bash
claude plugin install pr-review-toolkit
```

---

## 四、Git 提交类 Skills

### 4. `/commit` ⭐⭐⭐
**来源**：commit-commands 插件
**用途**：智能生成规范的 commit message

**安装**：
```bash
claude plugin install commit-commands
```

**使用**：
```bash
/commit
```

**功能**：
- ✅ 自动分析 git diff
- ✅ 生成符合约定式提交的 message
- ✅ 包含 Co-Authored-By: Claude

### 5. `/commit-push-pr` ⭐⭐⭐
**来源**：commit-commands 插件
**用途**：提交、推送并创建 PR

**使用**：
```bash
/commit-push-pr
```

### 6. `/clean_gone`
**来源**：commit-commands 插件
**用途**：清理已合并的本地分支

---

## 五、安全和规则类 Skills

### 7. `/hookify` ⭐⭐⭐⭐
**来源**：hookify 插件
**用途**：创建自定义行为保护规则

**安装**：
```bash
claude plugin install hookify
```

**使用**：
```bash
# 创建规则
/hookify 警告我使用 rm -rf 命令时
/hookify 编辑 .env 文件时警告

# 无参数时分析会话创建规则
/hookify
```

**子命令**：
```bash
/hookify:list        # 列出所有规则
/hookify:configure   # 交互式配置
/hookify:help        # 帮助信息
```

**规则类型**：
- **warn** - 警告但允许
- **block** - 阻止执行

**事件类型**：
- `bash` - Bash 命令
- `file` - 文件编辑（Edit/Write）
- `stop` - 会话结束前
- `prompt` - 用户提交 prompt
- `all` - 所有事件

**示例规则**：
```markdown
---
name: block-dangerous-rm
enabled: true
event: bash
pattern: rm\s+-rf
action: block
---

⚠️ **Dangerous rm command detected!**
```

---

## 六、前端开发类 Skills

### 8. `frontend-design` ⭐⭐⭐
**来源**：frontend-design 插件
**用途**：生成高质量前端界面

**安装**：
```bash
claude plugin install frontend-design
```

**使用**：
- 自动触发（无需命令）
- 描述界面需求即可

**示例**：
```
创建一个照片管理后台界面，包含：
- 照片列表和缩略图
- 筛选和搜索
- 批量操作
```

**特点**：
- ✅ 避免"AI 生成"的通用美学
- ✅ 大胆的设计选择
- ✅ 高质量动画和视觉细节
- ✅ 生产级代码

---

## 七、项目配置类 Skills

### 9. `/revise-claude-md` ⭐⭐⭐
**来源**：claude-md-management 插件
**用途**：改进和维护 CLAUDE.md 项目规范文件

**安装**：
```bash
claude plugin install claude-md-management
```

---

## 八、开发工具类 Skills

### 10. `/create-plugin` ⭐⭐
**来源**：plugin-dev 插件
**用途**：创建自定义 Claude Code 插件

**安装**：
```bash
claude plugin install plugin-dev
```

**相关 Skills**：
- `command-development` - 开发命令
- `skill-development` - 开发 skill
- `hook-development` - 开发 hook
- `agent-development` - 开发 agent
- `mcp-integration` - MCP 集成
- `plugin-settings` - 插件设置
- `plugin-structure` - 插件结构

---

## 九、其他实用 Skills

### 11. `/ralph-loop` ⭐⭐
**来源**：ralph-loop 插件
**用途**：自动化循环任务

**子命令**：
```bash
/ralph-loop
/cancel-ralph
/help
```

### 12. `/new-sdk-app`
**来源**：agent-sdk-dev 插件
**用途**：创建 Claude Agent SDK 应用

---

## 十、Skills 分类总结

### 按用途分类

#### 🏗️ **开发工作流**
1. `/feature-dev` - 完整功能开发流程
2. `/simplify` - 代码质量优化

#### 🔍 **代码审查**
3. `/code-review` - PR 自动审查
4. `/review-pr` - PR 审查工具

#### 📝 **Git 操作**
5. `/commit` - 智能提交
6. `/commit-push-pr` - 提交+PR
7. `/clean_gone` - 清理分支

#### 🛡️ **安全保护**
8. `/hookify` - 行为规则
9. `/hookify:list` - 列出规则
10. `/hookify:configure` - 配置规则

#### 🎨 **前端开发**
11. `frontend-design` - UI 设计（自动触发）

#### ⚙️ **项目配置**
12. `/revise-claude-md` - 维护规范文档

#### 🔧 **插件开发**
13. `/create-plugin` - 创建插件

---

## 十一、针对 Relive 项目的推荐

### 必装（优先级 1）⭐⭐⭐⭐⭐
```bash
# 1. Golang LSP（必需）
go install golang.org/x/tools/gopls@latest

# 2. 核心开发工具
claude plugin install feature-dev      # 系统化开发
claude plugin install hookify          # 安全保护
```

### 推荐（优先级 2）⭐⭐⭐⭐
```bash
claude plugin install code-review      # 代码审查
claude plugin install frontend-design  # Web 界面
claude plugin install commit-commands  # Git 提交规范
```

### 可选（优先级 3）⭐⭐⭐
```bash
claude plugin install claude-md-management  # CLAUDE.md 维护
claude plugin install pr-review-toolkit     # PR 工具集
```

---

## 十二、Skills 使用建议

### 开发阶段 → Skills 映射

| 阶段 | 推荐 Skills | 说明 |
|------|------------|------|
| 需求设计 | 无 | 专注文档 |
| 功能开发 | `/feature-dev` | 大功能使用 |
| 代码优化 | `/simplify` | 每次完成后必用 |
| 界面开发 | `frontend-design` | 自动触发 |
| 代码审查 | `/code-review` | PR 前使用 |
| Git 提交 | `/commit` | 规范化提交 |

### 工作流示例

#### 标准功能开发流程
```bash
# 1. 功能开发
/feature-dev 实现照片扫描服务

# 2. 代码优化
/simplify

# 3. 提交代码
/commit

# 4. 创建 PR（可选）
/commit-push-pr
```

#### 快速小功能流程
```bash
# 直接实现 + 优化 + 提交
（手动写代码）
/simplify
/commit
```

---

## 十三、查看和管理 Skills

### 查看可用插件
```bash
claude plugin list
```

### 查看已安装插件
```bash
claude plugin list --installed
```

### 安装插件
```bash
claude plugin install <plugin-name>
```

### 卸载插件
```bash
claude plugin uninstall <plugin-name>
```

### 查看插件详情
```bash
# 在插件目录查看 README
ls ~/.claude/plugins/marketplaces/claude-plugins-official/plugins/
```

---

## 十四、Skills 性能和成本

### Token 消耗（从低到高）

| Skills | Agent 数量 | Token 消耗 | 速度 |
|--------|-----------|-----------|------|
| `/simplify` | 1 | 低 | 快 |
| `/commit` | 0 | 极低 | 极快 |
| `/hookify` | 0 | 极低 | 即时 |
| `frontend-design` | 0 | 低 | 快 |
| `/code-review` | 4 | 中高 | 中等 |
| `/feature-dev` | 6-9 | 高 | 慢 |

### 优化建议
- ✅ 小功能：手动实现 + `/simplify`
- ✅ 大功能：`/feature-dev` 完整流程
- ✅ 频繁使用：`/simplify`、`/commit`
- ⚠️ 谨慎使用：`/feature-dev`（token 消耗大）

---

## 总结

**核心 Skills（必用）**：
1. ✅ `/simplify` - 内置，每次必用
2. ✅ `/feature-dev` - 大功能开发
3. ✅ `/hookify` - 安全保护
4. ✅ `frontend-design` - Web 界面
5. ✅ `/commit` - 规范提交

**推荐安装命令**：
```bash
go install golang.org/x/tools/gopls@latest
claude plugin install feature-dev hookify code-review frontend-design commit-commands
```

这些 Skills 将显著提升 Relive 项目的开发质量和效率！
