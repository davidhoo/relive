# Docker 部署离线逆地理编码数据导入指南

## 概述

Relive 支持离线逆地理编码，需要导入城市数据到数据库。本文档介绍 Docker 部署下的数据导入方案。

## 数据文件

城市数据来源于 [GeoNames](https://www.geonames.org/)，包含全球 500 人以上居住的城市。

### 下载数据

```bash
# 下载城市数据
wget https://download.geonames.org/export/dump/cities500.zip

# 解压
unzip cities500.zip

# 得到 cities500.txt 文件（约 150MB，包含 20 万+ 城市）
```

## 方案一：自动导入（推荐，已内置）

### 快速开始

```bash
# 1. 下载城市数据
mkdir -p data
cd data
wget https://download.geonames.org/export/dump/cities500.zip
unzip cities500.txt

# 2. 修改 docker-compose.yml，取消挂载注释
# - ./data/cities500.txt:/app/data/cities500.txt:ro

# 3. 启动服务，自动导入会在首次启动时执行
docker-compose up -d

# 4. 查看导入日志
docker-compose logs -f relive | grep -E "City|city|导入|import"
```

### 工作原理

容器启动时会自动执行以下检查：
1. 检查是否存在 `cities500.txt` 数据文件
2. 检查数据库中是否已有城市数据（>1000 条）
3. 如果没有数据，自动执行导入
4. 导入完成后启动主应用

### 配置说明

`docker-compose.yml` 已预设挂载（默认注释）：

```yaml
relive:
  volumes:
    # ... 其他挂载
    # 取消下方注释启用自动导入
    # - ./data/cities500.txt:/app/data/cities500.txt:ro
  environment:
    # 自动导入开关（默认: true）
    - AUTO_IMPORT_CITIES=${AUTO_IMPORT_CITIES:-true}
```

### 禁用自动导入

如果不需要离线地理编码：

```yaml
environment:
  - AUTO_IMPORT_CITIES=false
```

## 方案二：手动导入

如需手动控制导入时机：

### 进入容器执行导入
cd data
wget https://download.geonames.org/export/dump/cities500.zip
unzip cities500.zip
```

### 3. 修改后端入口点脚本

在 `backend/scripts/docker-entrypoint.sh` 中添加：

```bash
#!/bin/sh
set -e

# 如果启用了自动导入且数据文件存在
if [ "${AUTO_IMPORT_CITIES}" = "true" ] && [ -f "/app/data/cities500.txt" ]; then
    echo "Checking city data..."
    /app/init-cities.sh
fi

# 启动主应用
exec "$@"
```

### 4. 启动服务

```bash
docker-compose up -d

# 查看导入日志
docker-compose logs -f relive | grep -E "city|City|导入"
```

首次启动时会自动检测并导入城市数据，后续启动会跳过。

## 方案二：手动导入

### 进入容器执行导入

```bash
# 1. 复制数据文件到容器
docker cp cities500.txt relive:/app/data/

# 2. 进入容器
docker exec -it relive sh

# 3. 执行导入
/app/import-cities --file /app/data/cities500.txt --config /app/config.yaml

# 4. 退出容器
exit
```

### 验证导入结果

```bash
# 检查城市数量
docker exec relive sqlite3 /app/data/relive.db "SELECT COUNT(*) FROM cities;"

# 查看部分城市
docker exec relive sqlite3 /app/data/relive.db "SELECT name, country FROM cities LIMIT 10;"
```

## 方案三：预填充数据库（开发/测试）

### 在本地导入后挂载数据库

```bash
# 1. 本地开发环境导入数据
cd backend
go run cmd/import-cities/main.go --file cities500.txt

# 2. 复制数据库到 Docker 数据目录
cp data/relive.db ../data/relive.db.initial

# 3. 修改 docker-compose.yml 挂载预填充数据库
```

```yaml
volumes:
  # 使用预填充的数据库
  - ./data/relive.db.initial:/app/data/relive.db
```

## 配置说明

### 离线模式配置

在 `config.prod.yaml` 中启用离线地理编码：

```yaml
geocode:
  provider: "offline"  # 可选: offline, amap, nominatim
  offline:
    max_distance: 100  # 最大搜索距离（公里）
```

### 混合模式（推荐）

优先使用离线数据，失败时回退到在线服务：

```yaml
geocode:
  provider: "hybrid"
  fallback: "amap"  # 离线失败时使用高德
  amap:
    api_key: "${AMAP_API_KEY}"
```

## 数据更新

城市数据不会频繁变化，建议每 6-12 个月更新一次：

```bash
# 1. 下载最新数据
wget https://download.geonames.org/export/dump/cities500.zip
unzip -o cities500.zip

# 2. 复制到容器并重新导入
docker cp cities500.txt relive:/app/data/
docker exec relive /app/import-cities --file /app/data/cities500.txt --config /app/config.yaml
```

## 常见问题

### Q: 导入失败或卡住？

A: 可能是内存不足，cities500.txt 包含 20 万+ 记录，建议：
- 确保容器有足够内存（至少 1GB）
- 减小批量大小：`--batch 500`
- 使用更小的数据文件：cities15000.txt（1.5 万人以上城市）

### Q: 只想导入特定国家？

A: 可以过滤数据文件后导入：

```bash
# 只保留中国（CN）、日本（JP）、韩国（KR）
grep -E "\t(CN|JP|KR)\t" cities500.txt > cities_asia.txt
```

### Q: 如何验证离线地理编码工作正常？

A: 在系统设置中配置离线模式后，上传带 GPS 的照片，检查位置信息：

```bash
# 查看日志
docker-compose logs -f relive | grep -i geocode
```

## 数据表结构

城市数据存储在 `cities` 表：

```sql
CREATE TABLE cities (
    id INTEGER PRIMARY KEY,
    geoname_id INTEGER UNIQUE,
    name VARCHAR(200),
    admin_name VARCHAR(200),  -- 省/州
    country VARCHAR(100),      -- 国家代码
    latitude REAL,
    longitude REAL
);
```

索引：
- `idx_geoname_id` - GeoNames ID
- `idx_name` - 城市名
- `idx_lat`, `idx_lon` - 经纬度（范围查询）
