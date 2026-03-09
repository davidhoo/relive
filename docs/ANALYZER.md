# relive-analyzer 旧版文件模式说明（历史文档）

> 状态：历史参考，不代表当前实现
> 当前推荐：请使用 `docs/ANALYZER_API_MODE.md`

## 说明

本仓库早期曾设计并实现过基于 SQLite 文件交换的 analyzer 工作流：
- 从主服务导出 `export.db`
- 在离线机器上分析
- 再将结果导回主服务

当前仓库已将 analyzer 主流程切换为 **API 模式**：
- 不再以 `export.db` 作为默认交换介质
- 不再把导出 / 导入 API 作为当前主流程文档
- CLI 也不再以 `-db export.db` / `estimate` / `--input` / `--output` 作为当前接口

## 当前应该看哪里

- API 模式说明：`docs/ANALYZER_API_MODE.md`
- 配置模板：`analyzer.yaml.example`
- CLI 实现：`backend/cmd/relive-analyzer/main.go`
- 服务端 analyzer 路由：`backend/internal/api/v1/router/router.go`

## 为什么保留本文档

保留本文档仅用于解释项目演进背景：
- 为什么一些旧的设计文档会提到 `export.db`
- 为什么部分历史总结文档会提到“导出/导入 API”
- 为什么旧截图或旧命令示例与当前实现不一致

如果你正在部署或使用当前版本，请不要按旧的文件模式文档操作。
