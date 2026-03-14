#!/bin/sh
set -e

# Docker 入口点脚本
# 功能：
# 1. 检查配置文件，使用默认配置作为后备
# 2. 启动主应用（城市数据在启动时自动从嵌入数据导入）

# 配置文件路径
CONFIG_FILE="${CONFIG_FILE:-/app/config.yaml}"
DEFAULT_CONFIG="/app/config.base.yaml"

# 如果没有外部配置文件，使用默认配置
if [ ! -f "$CONFIG_FILE" ]; then
    echo "No external config found, using base config"
    CONFIG_FILE="$DEFAULT_CONFIG"
fi

echo "Using config: $CONFIG_FILE"

# 确保数据目录存在
mkdir -p /app/data/logs /app/data/photos

# 启动主应用
echo "Starting Relive..."
exec /app/relive --config "$CONFIG_FILE"
