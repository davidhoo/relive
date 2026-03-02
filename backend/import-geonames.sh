#!/bin/bash

# GeoNames 城市数据导入脚本
# 用于 Relive 离线地理编码功能

set -e

echo "================================================"
echo "Relive 城市数据库导入工具"
echo "================================================"
echo ""

# 检查参数
DATASET="${1:-cities500}"
DATA_DIR="./data/geonames"
FILE_NAME="${DATASET}.txt"

# 支持的数据集
case "$DATASET" in
  cities500|cities1000|cities5000|cities15000)
    echo "✓ 使用数据集: ${DATASET}"
    ;;
  *)
    echo "错误: 不支持的数据集 '$DATASET'"
    echo ""
    echo "支持的数据集:"
    echo "  cities500   - ~200,000 城市 (人口>500) - 推荐"
    echo "  cities1000  - ~140,000 城市 (人口>1000)"
    echo "  cities5000  - ~50,000 城市 (人口>5000)"
    echo "  cities15000 - ~25,000 城市 (人口>15000)"
    echo ""
    echo "使用方法:"
    echo "  ./import-geonames.sh [dataset]"
    echo ""
    echo "示例:"
    echo "  ./import-geonames.sh cities500    # 默认，推荐"
    echo "  ./import-geonames.sh cities1000"
    exit 1
    ;;
esac

# 创建数据目录
mkdir -p "$DATA_DIR"

# 下载数据
ZIP_FILE="${DATA_DIR}/${DATASET}.zip"
TXT_FILE="${DATA_DIR}/${FILE_NAME}"

if [ -f "$TXT_FILE" ]; then
  echo "✓ 数据文件已存在: $TXT_FILE"
  read -p "是否重新下载? (y/N) " -n 1 -r
  echo
  if [[ $REPLY =~ ^[Yy]$ ]]; then
    rm -f "$ZIP_FILE" "$TXT_FILE"
  else
    echo "✓ 跳过下载，使用现有文件"
  fi
fi

if [ ! -f "$TXT_FILE" ]; then
  echo "→ 下载 ${DATASET}.zip ..."
  DOWNLOAD_URL="https://download.geonames.org/export/dump/${DATASET}.zip"

  if command -v wget &> /dev/null; then
    wget -O "$ZIP_FILE" "$DOWNLOAD_URL"
  elif command -v curl &> /dev/null; then
    curl -L -o "$ZIP_FILE" "$DOWNLOAD_URL"
  else
    echo "错误: 需要 wget 或 curl 来下载文件"
    exit 1
  fi

  echo "✓ 下载完成"

  echo "→ 解压缩 ..."
  unzip -o "$ZIP_FILE" -d "$DATA_DIR"
  echo "✓ 解压完成"
fi

# 显示文件信息
FILE_SIZE=$(du -h "$TXT_FILE" | cut -f1)
LINE_COUNT=$(wc -l < "$TXT_FILE")
echo ""
echo "数据文件信息:"
echo "  路径: $TXT_FILE"
echo "  大小: $FILE_SIZE"
echo "  行数: $LINE_COUNT"
echo ""

# 导入数据库
echo "→ 导入数据库 ..."
echo "  这可能需要几分钟，请耐心等待..."
echo ""

go run cmd/import-cities/main.go --file "$TXT_FILE"

if [ $? -eq 0 ]; then
  echo ""
  echo "================================================"
  echo "✓ 导入完成!"
  echo "================================================"
  echo ""
  echo "下一步:"
  echo "  1. 在配置页面设置主要提供商为 'Offline (离线数据库)'"
  echo "  2. 扫描照片时将自动使用离线地理编码"
  echo ""
  echo "提示:"
  echo "  - 离线提供商查询速度最快 (<1ms)"
  echo "  - 无 API 调用限制"
  echo "  - 适合大批量扫描"
  echo ""
else
  echo ""
  echo "导入失败，请检查错误信息"
  exit 1
fi
