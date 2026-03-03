#!/bin/sh
# 初始化城市数据脚本（Docker 入口点使用）

DB_PATH="${DB_PATH:-/app/data/relive.db}"
CITIES_FILE="${CITIES_FILE:-/app/data/cities500.txt}"

# 检查是否已有城市数据
if [ -f "$DB_PATH" ]; then
    CITY_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM cities WHERE deleted_at IS NULL;" 2>/dev/null || echo "0")
    if [ "$CITY_COUNT" -gt "0" ]; then
        echo "City data already exists: $CITY_COUNT cities"
        exit 0
    fi
fi

# 检查数据文件是否存在
if [ ! -f "$CITIES_FILE" ]; then
    echo "Warning: Cities data file not found at $CITIES_FILE"
    echo "Please download from: wget https://download.geonames.org/export/dump/cities500.zip"
    exit 0
fi

echo "Importing city data from $CITIES_FILE..."

# 运行导入工具
/app/import-cities --file "$CITIES_FILE" --config /app/config.yaml

echo "City data import completed"
