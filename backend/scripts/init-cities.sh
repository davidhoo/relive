#!/bin/sh
# 初始化城市数据脚本（Docker 入口点使用）
set -e

CONFIG_FILE="${CONFIG_FILE:-/app/config.yaml}"
DB_PATH="${DB_PATH:-/app/data/relive.db}"
CITIES_FILE="${CITIES_FILE:-/app/data/cities500.txt}"
ALTERNATE_NAMES_FILE="${ALTERNATE_NAMES_FILE:-/app/data/alternateNamesV2.txt}"
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
# 方法1: 如果数据库文件存在，使用 SQLite 查询
if [ -f "$DB_PATH" ]; then
    # 检查 cities 表是否存在且有数据
    CITY_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM cities WHERE deleted_at IS NULL;" 2>/dev/null || echo "0")
    if [ "$CITY_COUNT" != "0" ] && [ "$CITY_COUNT" -gt "100000" ]; then
        echo "======================================"
        echo "City data already exists: $CITY_COUNT cities"
        echo "Skipping import to avoid duplicate data"
        echo "======================================"

        # 即使城市数据已存在，也检查是否需要导入中文名
        if [ -f "$ALTERNATE_NAMES_FILE" ]; then
            ZH_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM cities WHERE name_zh != '' AND name_zh IS NOT NULL;" 2>/dev/null || echo "0")
            if [ "$ZH_COUNT" = "0" ]; then
                echo "Chinese city names not found, importing..."
                /app/import-cities --alternate-names "$ALTERNATE_NAMES_FILE" --config "$CONFIG_FILE" || \
                    echo "Warning: Chinese city names import failed (non-fatal)"
            else
                echo "Chinese city names already exist: $ZH_COUNT entries"
            fi
        fi

        exit 0
    fi
fi

# 方法2: 使用导入程序检查（更可靠，支持所有数据库类型）
CHECK_RESULT=$(/app/import-cities --check --config "$CONFIG_FILE" 2>/dev/null || echo "0")
if [ "$CHECK_RESULT" != "0" ] && [ "$CHECK_RESULT" -gt "100000" ]; then
    echo "======================================"
    echo "City data already exists: $CHECK_RESULT cities"
    echo "Skipping import to avoid duplicate data"
    echo "======================================"
    exit 0
fi

echo "======================================"
echo "Importing city data..."
echo "Source: $CITIES_FILE"
echo "Database: $DB_PATH"
echo "======================================"

# 构建导入命令参数
IMPORT_ARGS="--file $CITIES_FILE --config $CONFIG_FILE"
if [ -f "$ALTERNATE_NAMES_FILE" ]; then
    IMPORT_ARGS="$IMPORT_ARGS --alternate-names $ALTERNATE_NAMES_FILE"
    echo "Also importing Chinese city names from: $ALTERNATE_NAMES_FILE"
fi

if /app/import-cities $IMPORT_ARGS; then
    echo "======================================"
    echo "City data import completed successfully"
    echo "======================================"
else
    echo "Warning: City data import failed, continuing without offline geocoding"
    echo "         You can manually import later using:"
    echo "         docker exec <container> /app/import-cities --file /app/data/cities500.txt --config $CONFIG_FILE"
fi
