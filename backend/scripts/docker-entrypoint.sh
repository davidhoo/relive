#!/bin/sh
set -e

# Docker 入口点脚本
# 功能：
# 1. 检查配置文件，使用默认配置作为后备
# 2. 自动导入城市数据（如果配置了 AUTO_IMPORT_CITIES）
# 3. 启动主应用

# 配置文件路径
CONFIG_FILE="${CONFIG_FILE:-/app/config.yaml}"
DEFAULT_CONFIG="/app/config.default.yaml"

# 如果没有外部配置文件，使用默认配置
if [ ! -f "$CONFIG_FILE" ]; then
    echo "No external config found, using default config"
    CONFIG_FILE="$DEFAULT_CONFIG"
fi

echo "Using config: $CONFIG_FILE"

DB_PATH="${DB_PATH:-/app/data/relive.db}"
CITIES_FILE="${CITIES_FILE:-/app/data/cities500.txt}"
AUTO_IMPORT="${AUTO_IMPORT_CITIES:-true}"

# 函数：检查并导入城市数据
import_cities_if_needed() {
    # 如果禁用了自动导入，跳过
    if [ "$AUTO_IMPORT" != "true" ]; then
        echo "Auto import disabled, skipping city data check"
        return 0
    fi

    # 检查数据文件是否存在
    if [ ! -f "$CITIES_FILE" ]; then
        echo "Info: Cities data file not found at $CITIES_FILE"
        echo "      Download from: wget https://download.geonames.org/export/dump/cities500.zip"
        return 0
    fi

    # 检查数据库是否已有城市数据
    local city_count=0
    if [ -f "$DB_PATH" ]; then
        city_count=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM cities WHERE deleted_at IS NULL;" 2>/dev/null || echo "0")
    fi

    if [ "$city_count" -gt "1000" ]; then
        echo "City data already exists: $city_count cities"
        return 0
    fi

    # 导入城市数据
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
}

# 确保数据目录存在
mkdir -p /app/data/logs /app/data/photos

# 检查并导入城市数据
import_cities_if_needed

# 启动主应用
echo "Starting Relive..."
exec /app/relive --config "$CONFIG_FILE"
