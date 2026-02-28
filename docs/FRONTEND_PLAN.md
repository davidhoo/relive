# Relive 前端开发计划

> 创建时间：2026-02-28
> 状态：规划中
> 目标：构建完整的 Web 管理后台

---

## 📋 技术栈

### 核心框架
- **Vue 3** - 前端框架（Composition API）
- **TypeScript** - 类型安全
- **Vite** - 构建工具（快速、现代）
- **Pinia** - 状态管理（Vue 3 官方推荐）
- **Vue Router** - 路由管理

### UI 组件库（待定）
**选项 1：Element Plus**
- ✅ 成熟稳定，组件丰富
- ✅ 中文文档完善
- ✅ TypeScript 支持好
- ✅ 适合管理后台

**选项 2：Ant Design Vue**
- ✅ 设计规范完善
- ✅ 组件质量高
- ✅ 企业级应用

**选项 3：Naive UI**
- ✅ TypeScript 友好
- ✅ 轻量级
- ✅ 现代化设计

**推荐：Element Plus**（组件最丰富，适合快速开发）

### 其他工具
- **Axios** - HTTP 请求
- **Day.js** - 日期处理
- **ECharts** / **Chart.js** - 数据可视化
- **Vue Query** / **SWR** - 数据获取和缓存（可选）

---

## 🏗️ 项目结构

```
frontend/
├── public/                 # 静态资源
├── src/
│   ├── assets/            # 资源文件
│   │   ├── images/
│   │   └── styles/
│   ├── api/               # API 接口封装
│   │   ├── photo.ts
│   │   ├── device.ts
│   │   ├── ai.ts
│   │   ├── display.ts
│   │   └── config.ts
│   ├── components/        # 公共组件
│   │   ├── PhotoCard.vue
│   │   ├── PhotoGrid.vue
│   │   ├── DeviceCard.vue
│   │   └── ...
│   ├── layouts/           # 布局组件
│   │   ├── MainLayout.vue
│   │   └── EmptyLayout.vue
│   ├── router/            # 路由配置
│   │   └── index.ts
│   ├── stores/            # Pinia 状态管理
│   │   ├── user.ts
│   │   ├── photo.ts
│   │   └── system.ts
│   ├── types/             # TypeScript 类型定义
│   │   ├── photo.ts
│   │   ├── device.ts
│   │   └── api.ts
│   ├── utils/             # 工具函数
│   │   ├── request.ts
│   │   ├── format.ts
│   │   └── validate.ts
│   ├── views/             # 页面组件
│   │   ├── Dashboard/     # 仪表盘
│   │   ├── Photos/        # 照片管理
│   │   ├── Analysis/      # AI 分析
│   │   ├── Devices/       # 设备管理
│   │   ├── Display/       # 展示策略
│   │   ├── Export/        # 导出/导入
│   │   ├── Config/        # 配置管理
│   │   └── System/        # 系统管理
│   ├── App.vue
│   └── main.ts
├── index.html
├── package.json
├── tsconfig.json
├── vite.config.ts
└── README.md
```

---

## 📱 页面规划

### 1. 仪表盘（Dashboard）
**路由**：`/`

**功能**：
- 系统概览
- 照片统计（总数、已分析、未分析）
- 设备统计（总数、在线数）
- 近期展示记录
- AI 分析进度
- 成本统计

**组件**：
- StatCard（统计卡片）
- ProgressBar（进度条）
- RecentDisplay（近期展示）
- CostChart（成本图表）

---

### 2. 照片管理（Photos）

#### 2.1 照片列表
**路由**：`/photos`

**功能**：
- 照片网格展示（瀑布流/网格）
- 筛选：已分析/未分析、位置、日期范围
- 排序：拍摄时间、评分
- 搜索：文件名、描述、标签
- 分页加载
- 批量操作

**组件**：
- PhotoGrid（照片网格）
- PhotoFilter（筛选器）
- PhotoSearch（搜索框）
- Pagination（分页）

#### 2.2 照片详情
**路由**：`/photos/:id`

**功能**：
- 照片预览（大图）
- EXIF 信息展示
- AI 分析结果
  - 描述
  - 文案
  - 分类
  - 标签
  - 评分（回忆、美观、综合）
  - 评分理由
- 展示历史
- 操作：重新分析、删除

**组件**：
- PhotoViewer（照片查看器）
- ExifInfo（EXIF 信息）
- AnalysisResult（分析结果）
- DisplayHistory（展示历史）

#### 2.3 照片扫描
**路由**：`/photos/scan`

**功能**：
- 扫描路径配置
- 扫描进度实时显示
- 扫描结果统计
- 错误日志

**组件**：
- ScanConfig（扫描配置）
- ScanProgress（扫描进度）
- ScanResult（扫描结果）

---

### 3. AI 分析（Analysis）

#### 3.1 分析管理
**路由**：`/analysis`

**功能**：
- 分析进度概览
- 未分析照片列表
- 批量分析操作
- Provider 切换
- 成本统计
- 分析历史

**组件**：
- AnalysisProgress（分析进度）
- UnanalyzedList（未分析列表）
- ProviderSelector（Provider 选择）
- CostStatistics（成本统计）

#### 3.2 Provider 配置
**路由**：`/analysis/provider`

**功能**：
- Provider 列表（5种）
- 当前 Provider 状态
- Provider 配置（API Key、Endpoint 等）
- 测试连接
- 性能对比

**组件**：
- ProviderList（Provider 列表）
- ProviderConfig（Provider 配置）
- ProviderTest（连接测试）

---

### 4. 设备管理（Devices）

#### 4.1 设备列表
**路由**：`/devices`

**功能**：
- 设备卡片展示
- 在线状态
- 设备信息（屏幕尺寸、电量、WiFi）
- 最后心跳时间
- 固件版本
- 操作：删除、重置

**组件**：
- DeviceGrid（设备网格）
- DeviceCard（设备卡片）
- DeviceStatus（设备状态）

#### 4.2 设备详情
**路由**：`/devices/:id`

**功能**：
- 设备详细信息
- 展示历史
- 心跳日志
- 设备统计

**组件**：
- DeviceInfo（设备信息）
- DisplayHistory（展示历史）
- HeartbeatLog（心跳日志）

---

### 5. 展示策略（Display）

**路由**：`/display`

**功能**：
- 当前展示照片预览
- 算法选择（往年今日/随机/高分）
- 参数配置
  - 刷新间隔
  - 避免重复天数
  - 降级策略
- 展示日历（查看历史展示）
- 手动触发展示

**组件**：
- CurrentDisplay（当前展示）
- AlgorithmSelector（算法选择）
- DisplayConfig（展示配置）
- DisplayCalendar（展示日历）

---

### 6. 导出/导入（Export）

#### 6.1 导出
**路由**：`/export`

**功能**：
- 导出路径配置
- 导出选项（全部/已分析）
- 导出进度
- 导出结果
- 下载导出文件

**组件**：
- ExportConfig（导出配置）
- ExportProgress（导出进度）
- ExportResult（导出结果）

#### 6.2 导入
**路由**：`/import`

**功能**：
- 导入文件选择
- 导入进度
- 导入结果（成功/失败）
- 错误日志

**组件**：
- ImportUpload（导入上传）
- ImportProgress（导入进度）
- ImportResult（导入结果）

---

### 7. 配置管理（Config）

**路由**：`/config`

**功能**：
- 配置列表（表格）
- 配置编辑（键值对）
- 配置分组（系统/显示/AI）
- 批量导入/导出
- 重置为默认

**组件**：
- ConfigTable（配置表格）
- ConfigEditor（配置编辑）
- ConfigGroup（配置分组）

---

### 8. 系统管理（System）

#### 8.1 系统信息
**路由**：`/system`

**功能**：
- 系统健康状态
- 版本信息
- 运行时间
- 数据库大小
- 照片存储统计

**组件**：
- SystemInfo（系统信息）
- HealthStatus（健康状态）

#### 8.2 日志查看
**路由**：`/system/logs`

**功能**：
- 日志列表
- 日志级别筛选
- 日志搜索
- 实时日志（WebSocket）

**组件**：
- LogViewer（日志查看器）
- LogFilter（日志筛选）

---

## 🎯 开发阶段

### Phase 1：项目搭建（1-2小时）
- [ ] 创建 Vite + Vue 3 + TypeScript 项目
- [ ] 安装依赖（Element Plus、Axios、Pinia、Vue Router）
- [ ] 配置路由
- [ ] 配置 Axios（API 基础地址、拦截器）
- [ ] 创建基础布局
- [ ] 配置 TypeScript 类型定义

### Phase 2：核心功能（第1天）
- [ ] 仪表盘页面
- [ ] 照片列表页面
- [ ] 照片详情页面
- [ ] API 接口封装

### Phase 3：AI 管理（第2天）
- [ ] AI 分析管理页面
- [ ] Provider 配置页面
- [ ] 批量分析功能
- [ ] 进度实时更新

### Phase 4：设备和展示（第3天）
- [ ] 设备管理页面
- [ ] 展示策略页面
- [ ] 导出/导入页面

### Phase 5：配置和系统（第4天）
- [ ] 配置管理页面
- [ ] 系统信息页面
- [ ] 优化和测试

---

## 🎨 设计规范

### 颜色方案
- **主色**：#409EFF（Element Plus 默认蓝）
- **成功**：#67C23A
- **警告**：#E6A23C
- **危险**：#F56C6C
- **信息**：#909399

### 布局
- **侧边栏宽度**：200px
- **顶部导航高度**：60px
- **内容区域**：padding 20px
- **卡片间距**：20px

### 响应式
- **桌面**：>1200px（完整功能）
- **平板**：768-1200px（部分隐藏）
- **移动**：<768px（简化布局）

---

## 🚀 性能优化

### 懒加载
- 路由懒加载
- 图片懒加载
- 组件按需加载

### 数据缓存
- API 响应缓存
- 状态持久化（LocalStorage）
- 图片缓存

### 打包优化
- 代码分割
- Tree Shaking
- 压缩和混淆

---

## 📝 待定事项

- [ ] 确定 UI 组件库（Element Plus / Ant Design Vue / Naive UI）
- [ ] 是否需要暗色主题
- [ ] 是否需要多语言支持
- [ ] 是否需要移动端适配
- [ ] 是否需要 PWA 支持

---

*最后更新：2026-02-28*
