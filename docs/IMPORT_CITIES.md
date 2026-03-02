# 离线地理编码数据导入

本目录包含导入 GeoNames 城市数据的工具，用于 Relive 的离线地理编码功能。

## 快速开始

### 方法一：使用自动导入脚本（推荐）

```bash
cd backend

# 使用默认数据集 (cities500 - 推荐)
./import-geonames.sh

# 或选择其他数据集
./import-geonames.sh cities1000
```

脚本会自动：
1. 下载 GeoNames 数据
2. 解压缩
3. 导入数据库
4. 显示导入统计

### 方法二：手动导入

```bash
# 1. 下载数据
wget https://download.geonames.org/export/dump/cities500.zip
unzip cities500.zip

# 2. 导入数据库
cd backend
go run cmd/import-cities/main.go --file cities500.txt
```

## 数据集选择

| 数据集 | 城市数量 | 人口阈值 | 文件大小 | 推荐用途 |
|--------|----------|----------|----------|----------|
| **cities500** | ~200,000 | >500 | ~24MB | **推荐** - 覆盖面广 |
| cities1000 | ~140,000 | >1000 | ~18MB | 平衡选择 |
| cities5000 | ~50,000 | >5000 | ~7MB | 主要城市 |
| cities15000 | ~25,000 | >15000 | ~4MB | 大城市 |

**推荐使用 cities500**: 提供最广泛的覆盖，对于照片地理编码最有用。

## 导入选项

```bash
go run cmd/import-cities/main.go \
  --file cities500.txt \
  --config config.dev.yaml \
  --batch 1000
```

参数说明：
- `--file`: GeoNames 数据文件路径（必需）
- `--config`: 配置文件路径（默认: config.dev.yaml）
- `--batch`: 批量插入大小（默认: 1000）

## 验证导入

```bash
# SQLite
sqlite3 data/relive.db "SELECT COUNT(*) FROM cities;"

# 或使用 SQL 客户端查询
SELECT COUNT(*) FROM cities;
SELECT * FROM cities LIMIT 10;
```

## 数据格式

GeoNames cities*.txt 文件格式（Tab 分隔）：

```
geonameid	name	asciiname	alternatenames	latitude	longitude	feature_class	feature_code	country_code	cc2	admin1_code	admin2_code	admin3_code	admin4_code	population	elevation	dem	timezone	modification_date
```

## 性能指标

导入 cities500 (~200,000 城市) 大约需要：
- 时间: 30-60 秒
- 磁盘空间: ~20MB (SQLite)
- 内存: <100MB

导入后查询性能：
- 平均查询时间: <1ms
- 适合大批量扫描

## 更新数据

GeoNames 数据定期更新。重新导入：

```bash
# 删除旧数据并重新导入
./import-geonames.sh cities500
```

脚本会自动清空现有数据并导入新数据。

## 数据来源

**GeoNames** (https://www.geonames.org/)
- 许可证: Creative Commons Attribution 4.0
- 数据质量: 社区维护，持续更新
- 覆盖范围: 全球

下载地址: https://download.geonames.org/export/dump/

## 配置使用

导入完成后，在 Web 配置页面：

1. 访问 `/config`
2. 找到"GPS 逆地理编码配置"
3. 设置主要提供商为"离线数据库 (Offline)"
4. 设置备用提供商（可选）
5. 点击"保存配置"

## 故障排查

### 问题：导入失败
- 检查文件路径是否正确
- 确认数据库连接正常
- 查看错误日志

### 问题：导入很慢
- 增加 `--batch` 参数值（如 5000）
- 检查磁盘空间是否充足
- 使用 SSD 会更快

### 问题：离线提供商不工作
- 确认 cities 表有数据: `SELECT COUNT(*) FROM cities;`
- 检查后端日志中的初始化信息
- 验证配置页面设置正确

## 高级用法

### 只导入特定国家

修改导入工具，添加国家过滤：

```go
// 只导入中国城市
if fields[8] != "CN" {
    return nil, fmt.Errorf("skipping non-CN city")
}
```

### 添加省份名称映射

GeoNames 提供 admin1Codes.txt 用于映射省份代码到名称。可以增强导入工具支持此功能。

### 自定义数据

您也可以准备自己的城市列表，按相同格式导入：

```sql
INSERT INTO cities (geoname_id, name, admin_name, country, latitude, longitude)
VALUES (1234567, '北京', '北京市', 'CN', 39.9042, 116.4074);
```

## 相关文档

- [地理编码系统文档](./GEOCODING.md)
- [配置管理 UI 说明](./GEOCODE_CONFIG_UI.md)
- [测试结果报告](./TEST_RESULTS_GEOCODING.md)

## 技术支持

遇到问题？
1. 查看后端日志: `tail -f data/logs/relive.log`
2. 检查数据库状态
3. 参考故障排查部分

## 许可证

导入工具代码: 项目许可证
GeoNames 数据: CC BY 4.0 (需标注来源)
