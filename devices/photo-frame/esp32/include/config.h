#ifndef CONFIG_H
#define CONFIG_H

#include <Arduino.h>

// 本地配置文件（覆盖默认配置）
#if __has_include("config_local.h")
#include "config_local.h"
#endif

// ===================== WiFi 配置 =====================
// 请在 config_local.h 中定义实际的 WiFi 密码
#ifndef WIFI_SSID
#define WIFI_SSID "your_wifi_ssid"
#endif

#ifndef WIFI_PASSWORD
#define WIFI_PASSWORD "your_wifi_password"
#endif

// 自定义 MAC 地址（可选）
// 支持两种格式：
// 1. 字符串格式（推荐）: "AA:BB:CC:DD:EE:FF"
// 2. 数组格式: {0x14, 0x2B, 0x2F, 0xEC, 0x0B, 0x04}
// 取消下面两行注释并设置后，将使用自定义 MAC 地址连接 WiFi
// #define USE_CUSTOM_MAC_ADDRESS
// #define CUSTOM_MAC_ADDRESS_STRING "AA:BB:CC:DD:EE:FF"

// ===================== 服务器配置 =====================
// 后端服务器地址
// 支持格式：
// - 纯主机名/IP: "192.168.1.100" 或 "your-server.local"
// - 带协议: "http://192.168.1.100" 或 "https://your-server.example.com"
#ifndef SERVER_HOST
#define SERVER_HOST "192.168.1.100"
#endif

#ifndef SERVER_PORT
#define SERVER_PORT 8080
#endif

// 设备 API Key（在管理后台创建设备时获得）
#ifndef DEVICE_API_KEY
#define DEVICE_API_KEY "your_device_api_key_here"
#endif

// ===================== 屏幕配置 =====================
// 7.3寸 E Ink Spectra 6 分辨率
#define SCREEN_WIDTH 800
#define SCREEN_HEIGHT 480

// SPI 引脚配置 (根据 ESP32-S3 实际连接修改)
#define EINK_BUSY   10
#define EINK_RST    11
#define EINK_DC     12
#define EINK_CS     13
#define EINK_MOSI   14
#define EINK_SCK    15

// ===================== 功能配置 =====================
// 刷新间隔（毫秒）- 默认5分钟
#define REFRESH_INTERVAL_MS 300000

// HTTP 请求超时（毫秒）
#define HTTP_TIMEOUT_MS 30000

// 最大重试次数
#define MAX_RETRY_COUNT 3

// 重试延迟（毫秒）
#define RETRY_DELAY_MS 5000

// ===================== 调试配置 =====================
#define DEBUG_SERIAL Serial
#define DEBUG_BAUDRATE 115200

// 日志级别: 0=ERROR, 1=WARN, 2=INFO, 3=DEBUG
#ifndef LOG_LEVEL
#define LOG_LEVEL 3
#endif

#endif // CONFIG_H
