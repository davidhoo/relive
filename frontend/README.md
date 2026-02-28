# Relive Frontend

基于 Vue 3 + TypeScript + Element Plus 的前端管理系统。

## 技术栈

- **框架**: Vue 3.5 (Composition API)
- **构建工具**: Vite 7.3
- **语言**: TypeScript 5.7
- **UI 组件库**: Element Plus 2.8
- **路由**: Vue Router 5.0
- **状态管理**: Pinia 2.2
- **HTTP 客户端**: Axios 1.7
- **日期处理**: Day.js 1.11

## 项目结构

```
frontend/
├── public/              # 静态资源
├── src/
│   ├── api/            # API 接口定义
│   │   ├── ai.ts       # AI 分析 API
│   │   ├── device.ts   # 设备管理 API
│   │   ├── photo.ts    # 照片管理 API
│   │   └── system.ts   # 系统信息 API
│   ├── assets/         # 资源文件
│   ├── components/     # 公共组件
│   ├── layouts/        # 布局组件
│   │   └── MainLayout.vue  # 主布局（侧边栏+顶栏）
│   ├── router/         # 路由配置
│   │   └── index.ts    # 路由定义
│   ├── stores/         # Pinia 状态管理
│   │   └── system.ts   # 系统状态
│   ├── types/          # TypeScript 类型定义
│   │   ├── api.ts      # API 响应类型
│   │   ├── ai.ts       # AI 相关类型
│   │   ├── device.ts   # 设备相关类型
│   │   ├── photo.ts    # 照片相关类型
│   │   └── system.ts   # 系统相关类型
│   ├── utils/          # 工具函数
│   │   └── request.ts  # Axios 封装
│   ├── views/          # 页面组件
│   │   ├── Analysis/   # AI 分析管理
│   │   ├── Config/     # 配置管理
│   │   ├── Dashboard/  # 仪表盘
│   │   ├── Devices/    # 设备管理
│   │   ├── Display/    # 展示策略
│   │   ├── Export/     # 导出/导入
│   │   ├── Photos/     # 照片管理
│   │   └── System/     # 系统信息
│   ├── App.vue         # 根组件
│   └── main.ts         # 入口文件
├── .env.development    # 开发环境变量
├── .env.production     # 生产环境变量
├── index.html          # HTML 模板
├── package.json        # 依赖配置
├── tsconfig.json       # TypeScript 配置
└── vite.config.ts      # Vite 配置
```

## 功能模块

### 1. 仪表盘 (Dashboard)
- 系统统计卡片（照片数、分析数、设备数、存储空间）
- AI 分析进度展示
- 最近照片网格展示

### 2. 照片管理 (Photos)
- 照片列表（网格展示）
- 搜索和筛选
- 照片扫描
- 照片详情页（含 AI 分析结果）

### 3. AI 分析 (Analysis)
- AI Provider 配置信息
- 批量分析任务
- 分析进度监控
- 自动轮询更新

### 4. 设备管理 (Devices)
- ESP32 设备列表
- 设备统计（总数、在线、离线）
- 设备详情查看

### 5. 展示策略 (Display)
- 算法选择（随机、评分、时间、智能）
- 评分阈值设置
- 刷新间隔配置

### 6. 导出/导入 (Export)
- 照片数据导出（SQLite）
- 分析结果导入

### 7. 配置管理 (Config)
- 配置项列表
- 配置增删改查
- 批量配置操作

### 8. 系统信息 (System)
- 系统健康状态
- 运行时信息
- 版本信息

## 开发指南

### 安装依赖

```bash
npm install
```

### 启动开发服务器

```bash
npm run dev
```

访问 http://localhost:5173

### 构建生产版本

```bash
npm run build
```

### 预览生产构建

```bash
npm run preview
```

## 环境变量

### 开发环境 (.env.development)
```
VITE_API_BASE_URL=http://localhost:8080/api/v1
```

### 生产环境 (.env.production)
```
VITE_API_BASE_URL=/api/v1
```

## API 集成

所有 API 请求通过 `src/utils/request.ts` 封装的 Axios 实例发送：

- 自动添加 baseURL
- 统一错误处理
- Element Plus 消息提示
- 类型安全的响应处理

示例：
```typescript
import { photoApi } from '@/api/photo'

// 获取照片列表
const res = await photoApi.getList({ page: 1, page_size: 20 })
console.log(res.data.items)
```

## 路由守卫

路由切换时自动更新页面标题：
```
{页面标题} - Relive
```

## 状态管理

使用 Pinia 进行状态管理：

```typescript
import { useSystemStore } from '@/stores/system'

const systemStore = useSystemStore()
await systemStore.fetchStats()
console.log(systemStore.stats)
```

## 组件库

使用 Element Plus UI 组件库：
- 卡片、表格、表单
- 分页、进度条
- 图标、标签、按钮
- 对话框、消息提示

所有图标已全局注册，可直接使用：
```vue
<el-icon><Picture /></el-icon>
```

## 样式规范

- 使用 scoped CSS
- 遵循 BEM 命名规范
- 响应式布局
- Element Plus 主题色

## 开发状态

✅ 基础架构搭建完成
✅ 8 个页面模块完成
✅ TypeScript 类型定义完成
✅ API 接口集成完成
✅ 路由配置完成
✅ 状态管理集成完成
✅ 编译构建成功

## 下一步

- 接入后端 API 进行真实数据测试
- 完善错误处理和边界情况
- 添加加载状态优化
- 优化移动端适配
- 添加单元测试

## License

MIT

