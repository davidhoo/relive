# 离线逆地理编码说明

## 概述

Relive 内置了离线逆地理编码数据（GeoNames cities500 + 中文地名），开箱即用，无需手动下载或导入任何文件。

## 工作原理

- 城市数据（约 23 万城市 + 4.2 万中文地名）预处理后嵌入在二进制文件中
- 首次启动时，如果数据库 `cities` 表少于 1000 行，会自动从嵌入数据导入
- 已有数据的数据库不会重复导入

## 配置

在 Web UI 的"配置管理"页面设置，或在 `config.prod.yaml` 中配置：

```yaml
geocode:
  provider: "offline"       # 离线模式（推荐）
  offline:
    max_distance: 100       # 最大搜索距离（公里）
```

### 混合模式

优先使用离线数据，失败时回退到在线服务：

```yaml
geocode:
  provider: "offline"
  fallback: "amap"          # 离线失败时使用高德
  amap:
    api_key: "your-key"
```

## 手动重新导入

如果城市数据损坏，可通过 API 重新导入（从嵌入数据恢复）：

```bash
curl -X POST http://localhost:8080/api/v1/config/cities-data/reload \
  -H "Authorization: Bearer <token>"
```

## 更新嵌入数据

当 GeoNames 发布新数据时，可重新生成嵌入数据：

```bash
cd backend

# 1. 下载最新源文件
wget https://download.geonames.org/export/dump/cities500.zip && unzip -o cities500.zip
wget https://download.geonames.org/export/dump/alternateNamesV2.zip && unzip -o alternateNamesV2.zip

# 2. 运行预处理工具
go run cmd/gen-geodata/main.go \
  -cities cities500.txt \
  -alt alternateNamesV2.txt \
  -out pkg/geodata/cities_zh.csv.gz

# 3. 重新构建
go build ./...
```

## 数据表结构

```sql
CREATE TABLE cities (
    id INTEGER PRIMARY KEY,
    geoname_id INTEGER UNIQUE,
    name VARCHAR(200),
    name_zh VARCHAR(200),      -- 中文名
    admin_name VARCHAR(200),   -- 省/州
    country VARCHAR(100),
    latitude REAL,
    longitude REAL
);
```
