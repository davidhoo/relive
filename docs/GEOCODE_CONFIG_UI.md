# GPS 逆地理编码配置管理功能

## 功能概述

在配置管理页面添加了 GPS 逆地理编码供应商的完整管理功能，用户可以通过 Web 界面配置和切换不同的地理编码服务提供商。

## 功能特性

### 1. 供应商选择
- **主要提供商**: 选择首选的地理编码服务
  - 离线数据库 (Offline) - 最快，无API调用
  - 高德地图 (AMap) - 中国地区优选
  - OpenStreetMap (Nominatim) - 全球覆盖，免费

- **备用提供商**: 主提供商失败时自动切换
  - 支持所有提供商作为备用
  - 可选择"无备用"

### 2. 缓存管理
- **启用/禁用缓存**: 开关控制
- **缓存有效期**: 可配置 1-168 小时 (默认 24 小时)
- **性能提升**: 缓存可减少 95%+ 的 API 调用

### 3. 提供商配置

#### 高德地图 (AMap)
- **API Key**: 输入高德地图 API 密钥
  - 密码输入框，保护敏感信息
  - 提供"申请"按钮直达申请页面
- **超时时间**: 5-60 秒可调 (默认 10 秒)
- **适用场景**: 中国境内照片定位

#### Nominatim (OpenStreetMap)
- **服务端点**: 可自定义 Nominatim 服务地址
  - 默认: `https://nominatim.openstreetmap.org/reverse`
  - 支持自建 Nominatim 服务器
- **超时时间**: 5-60 秒可调 (默认 10 秒)
- **适用场景**: 全球照片定位，免费服务

#### 离线数据库
- **最大搜索距离**: 10-500 公里可调 (默认 100 公里)
- **说明提示**: 提供数据源链接 (GeoNames)
- **适用场景**: 快速本地查询，无网络依赖

### 4. 实时保存
- **保存配置** 按钮: 点击即时生效
- **加载状态**: 显示保存进度
- **错误提示**: 保存失败时显示详细错误信息

## 界面设计

### 布局结构
```
配置管理页面
├── 扫描路径配置 (原有)
└── GPS 逆地理编码配置 (新增)
    ├── 卡片头部
    │   ├── 图标 + 标题
    │   └── 保存配置按钮
    └── 表单内容
        ├── 主要提供商选择
        ├── 备用提供商选择
        ├── 缓存设置
        │   ├── 启用缓存开关
        │   └── 缓存有效期设置
        ├── 高德地图配置
        │   ├── API Key (带申请按钮)
        │   └── 超时时间
        ├── Nominatim 配置
        │   ├── 服务端点
        │   └── 超时时间
        └── 离线数据库配置
            ├── 最大搜索距离
            └── 说明提示
```

### 视觉特点
- **清晰分组**: 使用分隔线区分不同提供商配置
- **图标指引**: 每个分组配有图标增强识别
- **标签提示**: 重要选项带有标签（最快、中国优选、全球覆盖）
- **帮助文本**: 每个配置项下方显示简短说明
- **信息提示**: 离线数据库配置带有详细说明 Alert

## API 接口

### 前端 API (`src/api/config.ts`)

```typescript
// 获取地理编码配置
configApi.getGeocodeConfig(): Promise<GeocodeConfig>

// 更新地理编码配置
configApi.updateGeocodeConfig(config: GeocodeConfig): Promise<void>
```

### 后端 API

```
GET  /api/v1/config/geocode      - 读取 geocode 配置
PUT  /api/v1/config/geocode      - 更新 geocode 配置
```

配置以 JSON 格式存储在 `app_config` 表的 `geocode` key 中。

## 数据结构

```typescript
interface GeocodeConfig {
  // 提供商选择
  provider: string          // 主要提供商: offline / amap / nominatim
  fallback: string          // 备用提供商: offline / amap / nominatim / ""

  // 缓存设置
  cache_enabled: boolean    // 是否启用缓存
  cache_ttl: number        // 缓存有效期（秒）

  // AMap 配置
  amap_api_key: string     // API Key
  amap_timeout: number     // 超时时间（秒）

  // Nominatim 配置
  nominatim_endpoint: string  // 服务端点
  nominatim_timeout: number   // 超时时间（秒）

  // Offline 配置
  offline_max_distance: number  // 最大搜索距离（公里）
}
```

## 使用流程

### 配置高德地图
1. 访问 https://lbs.amap.com/ 申请 API Key
2. 在配置页面点击"保存配置"卡片
3. 展开"高德地图 (AMap) 配置"
4. 输入 API Key
5. 设置主要提供商为"高德地图 (AMap)"
6. 点击"保存配置"
7. 后续扫描照片时自动使用高德地图服务

### 配置 Nominatim
1. 保持默认端点或输入自建服务地址
2. 设置主要提供商为"OpenStreetMap (Nominatim)"
3. 点击"保存配置"
4. 无需 API Key，立即可用

### 配置离线数据库
1. 导入城市数据库到后端
   ```bash
   # 下载 GeoNames cities500.zip (包含人口>500的城市，覆盖面更广)
   wget https://download.geonames.org/export/dump/cities500.zip
   unzip cities500.zip

   # 导入数据库
   go run cmd/import-cities/main.go --file cities500.txt
   ```
2. 在配置页面设置主要提供商为"离线数据库 (Offline)"
3. 调整最大搜索距离（可选）
4. 点击"保存配置"

### 配置备用方案
建议的备用配置组合：

**中国用户**:
- 主要: AMap (高德地图)
- 备用: Offline (离线数据库)

**全球用户**:
- 主要: Nominatim (OpenStreetMap)
- 备用: Offline (离线数据库)

**离线优先**:
- 主要: Offline (离线数据库)
- 备用: Nominatim (OpenStreetMap)

## 配置生效

配置保存后：
- ✅ **立即生效**: 下次照片扫描时使用新配置
- ✅ **无需重启**: 配置通过 API 动态读取
- ✅ **持久化**: 配置保存在数据库中

## 注意事项

### AMap (高德地图)
- 需要注册并申请 API Key
- 免费版有每日调用次数限制（通常 10,000 次/天）
- 中国境内定位精准度最高
- 超出配额后会自动fallback到备用提供商

### Nominatim (OpenStreetMap)
- 官方服务有 1 req/sec 速率限制
- 批量扫描时速度较慢但稳定
- 完全免费，无配额限制
- 可自建服务器去除速率限制

### 离线数据库
- 需要手动导入城市数据库
- 数据库为空时自动跳过此提供商
- 查询速度最快（<1ms）
- 精度较低（城市级别）

## 故障排查

### 配置保存失败
- 检查后端服务是否运行
- 查看浏览器控制台错误信息
- 验证网络连接

### 地理编码不工作
1. 检查提供商配置是否正确
2. 验证 API Key 是否有效（AMap）
3. 确认照片包含 GPS 坐标
4. 查看后端日志中的错误信息

### 离线提供商不可用
- 确认已导入城市数据库
- 检查 `cities` 表是否有数据
- 查看后端启动日志

## 开发信息

### 新增文件
- `frontend/src/api/config.ts` - 添加地理编码 API 接口

### 修改文件
- `frontend/src/views/Config/index.vue` - 添加地理编码配置 UI

### 依赖
- Element Plus - UI 组件库
- Vue 3 - 前端框架
- TypeScript - 类型系统

## 未来改进

- [ ] 提供商状态检测（在线/离线）
- [ ] 实时测试提供商连接
- [ ] 显示各提供商使用统计
- [ ] 缓存命中率可视化
- [ ] 批量重新地理编码功能
- [ ] 配置导入/导出功能
- [ ] 自动选择最佳提供商
- [ ] 更多提供商支持（Google Maps、Photon等）
