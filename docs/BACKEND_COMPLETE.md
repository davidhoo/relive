# 后端能力快照

> 更新日期：2026-03-09
> 状态：与当前实现对齐的简化快照
> 源码真值：`backend/internal/api/v1/router/router.go`

## 概览

当前后端以 Gin + GORM + SQLite 为核心，已经集成：
- 认证与首次登录流程
- 系统统计与系统还原
- 照片扫描 / 重建 / 清理 / 查询
- AI 分析与后台任务
- 缩略图任务
- GPS 地理编码任务
- 展示策略、每日展示批次与渲染规格
- 设备管理
- analyzer API 模式
- 配置管理与提示词管理
- 离线城市数据下载导入

## 认证模型

- 公开接口：健康检查、环境信息、登录
- JWT 接口：Web 管理后台
- API Key 接口：设备与 analyzer
- 混合认证接口：图片与展示资源访问

## 当前路由分组

- `auth`
- `system`
- `devices`
- `display`
- `device`
- `analyzer`
- `photos`
- `thumbnails`
- `geocode`
- `ai`
- `config`

## 与旧总结文档的差异

以下内容已经不再适合作为当前实现说明：
- “导出/导入 API 已完整支持”
- “ExportService / ExportHandler 是当前主流程的一部分”
- “扫描/重建接口同步返回统计结果”
- “后端总接口数为 26 或 28”

当前更准确的描述是：
- analyzer 已切换为 API 模式
- 导出 / 导入不再是当前默认工作流
- 扫描 / 重建是异步任务接口
- 路由规模已明显超过早期阶段总结中的数量级

## 推荐查看顺序

1. `docs/BACKEND_API.md`
2. `backend/internal/api/v1/router/router.go`
3. `backend/internal/api/v1/handler/`
4. `backend/internal/service/`
