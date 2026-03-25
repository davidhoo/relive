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

// 办公室 SSID（config_local.h 覆盖，不入 Git）
// 扫描到此 SSID 时使用编译时凭据连接（办公室模式）
#ifndef OFFICE_SSID
#define OFFICE_SSID ""
#endif

// 调试模式（取消注释启用，启用后使用固定间隔而非计划调度）
// #define DEBUG_MODE

// 默认刷新计划（办公室模式 & NVS 无计划时的兜底）
#ifndef DEFAULT_SCHEDULES
#define DEFAULT_SCHEDULES "0800,2000"
#endif

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
#define EINK_BUSY   40
#define EINK_RST    41
#define EINK_DC     39
#define EINK_CS     38
#define EINK_MOSI   47
#define EINK_SCK    48

// ===================== 功能配置 =====================
// 刷新间隔（毫秒）- 默认5分钟，DEBUG_MODE 和时间无效时使用
#ifndef REFRESH_INTERVAL_MS
#define REFRESH_INTERVAL_MS 300000
#endif

// HTTP 请求超时（毫秒）
#define HTTP_TIMEOUT_MS 30000

// 最大重试次数
#define MAX_RETRY_COUNT 3

// 重试延迟（毫秒）
#define RETRY_DELAY_MS 5000

// ===================== AP 配网配置 =====================
// AP 热点 SSID（无密码）
#define AP_SSID            "relive"

// AP 超时时间（毫秒）- 3 分钟无设备连接则超时
#define AP_TIMEOUT_MS      180000

// AP 退避睡眠时间（分钟）
#define AP_BACKOFF_MINUTES {30, 60, 180}
#define AP_BACKOFF_STEPS   3

// WiFi 连续失败次数阈值（触发 AP 配网）
#define MAX_WIFI_RETRIES   10

// ===================== NTP 配置 =====================
#define NTP_SERVER         "pool.ntp.org"
#define GMT_OFFSET_SEC     28800    // GMT+8
#define DST_OFFSET_SEC     0

// 最小睡眠保护（秒）- 小于此值则跳到下一个计划点
#define MIN_SLEEP_SEC      60

// ===================== 调试配置 =====================
#define DEBUG_SERIAL Serial
#define DEBUG_BAUDRATE 115200

// 日志级别: 0=OFF, 1=ERROR, 2=INFO, 3=DEBUG
#ifndef LOG_LEVEL
#define LOG_LEVEL 3
#endif

#endif // CONFIG_H
