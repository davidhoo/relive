/**
 * @file config.h
 * @brief ESP32 墨水屏相框配置
 *
 * 从 config_local.h 加载本地配置（不在版本控制中）
 */

#ifndef CONFIG_H
#define CONFIG_H

// 尝试加载本地配置
#if __has_include("config_local.h")
    #include "config_local.h"
#else
    // 默认配置（开发环境）
    #define WIFI_SSID           "your-wifi-ssid"
    #define WIFI_PASSWORD       "your-wifi-password"
    #define API_BASE_URL        "http://192.168.1.100:8080/api/v1"
    #define DEVICE_API_KEY      "your-device-api-key"
#endif

// ============ 硬件配置 ============

// 墨水屏引脚定义 (ESP32-S3)
#define EPD_PIN_CS          10
#define EPD_PIN_SCK         11
#define EPD_PIN_MOSI        12
#define EPD_PIN_DC          13
#define EPD_PIN_RST         14
#define EPD_PIN_BUSY        15

// LED 指示灯
#define LED_PIN             2

// 按键（用于手动刷新）
#define BUTTON_PIN          0

// ============ 网络配置 ============

#define WIFI_CONNECT_TIMEOUT_MS     30000
#define WIFI_RECONNECT_ATTEMPTS     3

// 自定义 MAC 地址（如果 config_local.h 中定义了则使用）
#ifndef WIFI_CUSTOM_MAC
    #define WIFI_USE_CUSTOM_MAC     0
#else
    #define WIFI_USE_CUSTOM_MAC     1
#endif

// HTTP 配置
#define HTTP_TIMEOUT_MS             60000
#define HTTP_BUFFER_SIZE            4096

// ============ API 配置 ============

#define API_ENDPOINT_DISPLAY_BIN    "/device/display.bin"
#define API_HEADER_API_KEY          "X-API-Key"

// ============ 显示配置 ============

// 默认渲染规格
#define DISPLAY_WIDTH               480
#define DISPLAY_HEIGHT              800
#define DISPLAY_COLOR_DEPTH         4

// 刷新间隔（毫秒）
#define REFRESH_INTERVAL_MS         60000       // 1分钟
#define REFRESH_INTERVAL_DEEP_SLEEP 3600000000ULL // 1小时（微秒，用于深度睡眠）

// ============ 电源管理 ============

// 启用深度睡眠
#define ENABLE_DEEP_SLEEP           1

// 电池电量检测（ADC）
#define BATTERY_ADC_PIN             4
#define BATTERY_ADC_DIVIDER         2.0f

// 低电量阈值（百分比）
#define BATTERY_LOW_THRESHOLD       20

// ============ 调试配置 ============

// 日志级别: 0=NONE, 1=ERROR, 2=WARN, 3=INFO, 4=DEBUG
#define LOG_LEVEL                   3

// 串口波特率
#define SERIAL_BAUD_RATE            115200

// ============ 缓冲区配置 ============

// Bin 文件最大大小（800 * 480 * 2 约 768KB，留有余量）
#define BIN_FILE_MAX_SIZE           (1024 * 1024)

// 分块下载缓冲区大小
#define DOWNLOAD_CHUNK_SIZE         4096

#endif // CONFIG_H
