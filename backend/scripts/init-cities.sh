#!/bin/sh
# 初始化城市数据脚本（Docker 入口点使用）

CONFIG_FILE="${CONFIG_FILE:-/app/config.yaml}"
DB_PATH="${DB_PATH:-/app/data/relive.db}"
CITIES_FILE="${CITIES_FILE:-/app/data/cities500.txt}"
AUTO_IMPORT="${AUTO_IMPORT_CITIES:-true}"

if [ "$AUTO_IMPORT" != "true" ]; then
    echo "Auto import disabled, skipping city data check"
    exit 0
fi

# 检查数据文件是否存在
if [ ! -f "$CITIES_FILE" ]; then
    echo "Warning: Cities data file not found at $CITIES_FILE"
    echo "Please download from: wget https://download.geonames.org/export/dump/cities500.zip"
    exit 0
fi

# 检查是否已有城市数据
if [ -f "$DB_PATH" ]; then
    CITY_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM cities WHERE deleted_at IS NULL;" 2>/dev/null || echo "0")
    if [ "$CITY_COUNT" -gt "0" ]; then
        echo "City data already exists: $CITY_COUNT cities"
        exit 0
    fi
fi

echo "======================================"
echo "Importing city data..."
echo "Source: $CITIES_FILE"
echo "Database: $DB_PATH"
echo "======================================"

if /app/import-cities --file "$CITIES_FILE" --config "$CONFIG_FILE"; then
    echo "======================================"
    echo "City data import completed successfully"
    echo "======================================"
else
    echo "Warning: City data import failed, continuing without offline geocoding"
    echo "         You can manually import later using:"
    echo "         docker exec <container> /app/import-cities --file /app/data/cities500.txt --config $CONFIG_FILE"
fi
