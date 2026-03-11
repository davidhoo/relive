#include <Arduino.h>
#include <HTTPClient.h>
#include <WiFiClientSecure.h>
#include "config.h"
#include "wifi_manager.h"
#include "api_client.h"
#include "display_driver.h"

// 全局对象
WiFiManager wifiManager;
APIClient apiClient;
DisplayDriver display;

// 图像缓冲区
// 服务器返回 4bit 格式：每2个像素1字节
// 800x480 屏幕需要: 800 * 480 / 2 = 192000 bytes
uint8_t* imageBuffer = nullptr;
const size_t BUFFER_SIZE_WITH_PSRAM = 200000;  // PSRAM 可用时的缓冲区 (195KB)
const size_t BUFFER_SIZE_NO_PSRAM = 200000;    // 无 PSRAM 时的缓冲区（192000 + 余量）
size_t actualBufferSize = 0;  // 实际分配的缓冲区大小

// 状态变量
enum SystemState {
    STATE_INIT,
    STATE_CONNECTING_WIFI,
    STATE_CONNECTED,
    STATE_DOWNLOADING,
    STATE_DISPLAYING,
    STATE_SLEEPING,
    STATE_ERROR
};
SystemState currentState = STATE_INIT;

// 统计信息
struct {
    int successCount = 0;
    int errorCount = 0;
    unsigned long lastRefreshTime = 0;
} stats;

// 日志宏
#if LOG_LEVEL >= 1
#define LOG_ERROR(msg) DEBUG_SERIAL.println(msg)
#define LOG_ERROR_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_ERROR(msg)
#define LOG_ERROR_F(msg, ...)
#endif

#if LOG_LEVEL >= 2
#define LOG_INFO(msg) DEBUG_SERIAL.println(msg)
#define LOG_INFO_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_INFO(msg)
#define LOG_INFO_F(msg, ...)
#endif

#if LOG_LEVEL >= 3
#define LOG_DEBUG(msg) DEBUG_SERIAL.println(msg)
#define LOG_DEBUG_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_DEBUG(msg)
#define LOG_DEBUG_F(msg, ...)
#endif

// 显示启动画面
void showStartupScreen() {
    DEBUG_SERIAL.println("\n=================================");
    DEBUG_SERIAL.println("   Relive 智能相框");
    DEBUG_SERIAL.println("   ESP32-S3 + E Ink Spectra 6");
    DEBUG_SERIAL.println("=================================\n");
}

// 分配缓冲区
bool allocateBuffer() {
    if (imageBuffer != nullptr) {
        return true; // 已分配
    }

    DEBUG_SERIAL.println("\n[System] ===== 内存分配诊断 =====");
    
    // 显示当前内存状态
    DEBUG_SERIAL.printf("[System] 总堆内存: %d bytes\n", ESP.getHeapSize());
    DEBUG_SERIAL.printf("[System] 可用堆内存: %d bytes\n", ESP.getFreeHeap());
    DEBUG_SERIAL.printf("[System] 最大可分配块: %d bytes\n", ESP.getMaxAllocHeap());
    
    // 检查 PSRAM
    bool hasPsram = psramFound();
    if (hasPsram) {
        DEBUG_SERIAL.printf("[System] PSRAM 可用: %d bytes\n", ESP.getPsramSize());
        DEBUG_SERIAL.printf("[System] PSRAM 空闲: %d bytes\n", ESP.getFreePsram());
        actualBufferSize = BUFFER_SIZE_WITH_PSRAM;
        imageBuffer = (uint8_t*)ps_malloc(actualBufferSize);
        if (imageBuffer != nullptr) {
            DEBUG_SERIAL.printf("[System] ✓ 从 PSRAM 分配成功: %d bytes\n", actualBufferSize);
            return true;
        }
        DEBUG_SERIAL.println("[System] ✗ PSRAM 分配失败");
    } else {
        DEBUG_SERIAL.println("[System] ⚠ PSRAM 未检测到");
    }

    // 尝试从内部 RAM 分配较小的缓冲区
    actualBufferSize = BUFFER_SIZE_NO_PSRAM;
    DEBUG_SERIAL.printf("[System] 尝试从内部 RAM 分配较小缓冲区: %d bytes\n", actualBufferSize);
    
    // 检查是否有足够的内存
    size_t freeHeap = ESP.getFreeHeap();
    size_t maxAlloc = ESP.getMaxAllocHeap();
    
    if (maxAlloc < actualBufferSize) {
        DEBUG_SERIAL.printf("[System] ✗ 最大可分配块 (%d bytes) 小于所需 (%d bytes)\n",
                          maxAlloc, actualBufferSize);
        DEBUG_SERIAL.println("[System] 建议:");
        DEBUG_SERIAL.println("  1. 启用 PSRAM (在 platformio.ini 中添加 board_build.arduino.memory_type = qio_opi)");
        DEBUG_SERIAL.println("  2. 或减小 BUFFER_SIZE_NO_PSRAM 的值");
        return false;
    }
    
    imageBuffer = (uint8_t*)malloc(actualBufferSize);

    if (imageBuffer == nullptr) {
        DEBUG_SERIAL.println("[System] ✗ 内存分配失败!");
        DEBUG_SERIAL.printf("[System] 请求: %d bytes, 可用: %d bytes, 最大块: %d bytes\n",
                          actualBufferSize, freeHeap, maxAlloc);
        DEBUG_SERIAL.println("[System] 可能原因:");
        DEBUG_SERIAL.println("  - 内存碎片化");
        DEBUG_SERIAL.println("  - 其他组件占用过多内存");
        DEBUG_SERIAL.println("  - 需要启用 PSRAM");
        return false;
    }

    DEBUG_SERIAL.printf("[System] ✓ 从内部 RAM 分配成功: %d bytes\n", actualBufferSize);
    DEBUG_SERIAL.printf("[System] 分配后可用堆内存: %d bytes\n", ESP.getFreeHeap());
    DEBUG_SERIAL.println("[System] ===========================\n");
    return true;
}

// 释放缓冲区
void freeBuffer() {
    if (imageBuffer != nullptr) {
        free(imageBuffer);
        imageBuffer = nullptr;
    }
}


// 下载并显示照片
bool downloadAndDisplay() {
    LOG_INFO("[Main] 开始下载照片...");

    // 检查 WiFi 连接
    if (!wifiManager.isConnected()) {
        LOG_ERROR("[Main] WiFi 未连接");
        return false;
    }

    // 获取显示信息（可选，用于日志）
    DisplayInfo info = apiClient.getDisplayInfo();
    if (info.valid) {
        LOG_INFO_F("[Main] 照片 ID: %d, Asset ID: %d\n", info.photoID, info.assetID);
    }

    // 下载 bin 文件
    String receivedChecksum;
    int downloaded = apiClient.downloadBinFile(imageBuffer, actualBufferSize, receivedChecksum);

    if (downloaded <= 0) {
        LOG_ERROR_F("[Main] 下载失败: %s\n", apiClient.getLastError().c_str());
        stats.errorCount++;
        return false;
    }

    LOG_INFO_F("[Main] 下载成功: %d bytes\n", downloaded);

    // 刷新显示（测试：先不旋转，直接显示）
    LOG_INFO("[Main] 刷新屏幕（直接显示，不旋转）...");
    display.display(imageBuffer, downloaded);
    
    // 如果需要旋转显示，使用下面这行：
    // display.displayRotated(imageBuffer, downloaded);

    stats.successCount++;
    stats.lastRefreshTime = millis();

    LOG_INFO("[Main] 显示完成");
    return true;
}

// 进入睡眠模式
void enterSleep() {
    LOG_INFO("[Main] 进入睡眠模式...");

    // 屏幕睡眠
    display.sleep();

    // 断开 WiFi 以节省电量
    wifiManager.disconnect();

    currentState = STATE_SLEEPING;

    // 配置唤醒源（定时器）
    esp_sleep_enable_timer_wakeup((uint64_t)REFRESH_INTERVAL_MS * 1000ULL);

    DEBUG_SERIAL.println("[Main] 进入 deep sleep...");
    DEBUG_SERIAL.flush();

    esp_deep_sleep_start();
}

// 错误处理
void handleError(const char* message) {
    LOG_ERROR_F("[Error] %s\n", message);
    currentState = STATE_ERROR;

    // 等待一段时间后重启
    delay(10000);
    ESP.restart();
}

void setup() {
    // 初始化串口
    DEBUG_SERIAL.begin(DEBUG_BAUDRATE);
    delay(1000); // 等待串口稳定

    showStartupScreen();

    // 检查唤醒原因
    esp_sleep_wakeup_cause_t wakeup_reason = esp_sleep_get_wakeup_cause();
    if (wakeup_reason == ESP_SLEEP_WAKEUP_TIMER) {
        DEBUG_SERIAL.println("[Main] 从定时器唤醒");
    } else if (wakeup_reason == ESP_SLEEP_WAKEUP_EXT0) {
        DEBUG_SERIAL.println("[Main] 从外部中断唤醒");
    } else {
        DEBUG_SERIAL.println("[Main] 正常启动/复位");
    }

    // 分配缓冲区
    if (!allocateBuffer()) {
        handleError("内存分配失败");
        return;
    }

    // 初始化显示
    if (!display.begin()) {
        handleError("显示初始化失败");
        return;
    }

    // 首次启动清屏
    if (wakeup_reason != ESP_SLEEP_WAKEUP_TIMER) {
        DEBUG_SERIAL.println("[Main] 首次启动，清屏...");
        display.clear();
    }

    // 初始化 API 客户端
    apiClient.begin();

    currentState = STATE_CONNECTING_WIFI;
}

void loop() {
    switch (currentState) {
        case STATE_CONNECTING_WIFI: {
            DEBUG_SERIAL.println("[Main] 连接 WiFi...");

            if (wifiManager.begin()) {
                currentState = STATE_CONNECTED;
            } else {
                // 重试
                delay(RETRY_DELAY_MS);
            }
            break;
        }

        case STATE_CONNECTED: {
            // 检查 WiFi 是否仍然连接
            if (!wifiManager.isConnected()) {
                LOG_ERROR("[Main] WiFi 断开，尝试重连...");
                if (!wifiManager.reconnect()) {
                    delay(RETRY_DELAY_MS);
                }
                break;
            }

            currentState = STATE_DOWNLOADING;
            break;
        }

        case STATE_DOWNLOADING: {
            if (downloadAndDisplay()) {
                currentState = STATE_DISPLAYING;
            } else {
                // 下载失败，稍后重试
                delay(RETRY_DELAY_MS);
                // 如果多次失败，进入睡眠等待下次唤醒
                if (stats.errorCount >= MAX_RETRY_COUNT) {
                    LOG_ERROR("[Main] 多次失败，进入睡眠");
                    enterSleep();
                }
            }
            break;
        }

        case STATE_DISPLAYING: {
            LOG_INFO_F("[Main] 成功: %d, 失败: %d\n", stats.successCount, stats.errorCount);

            // 进入睡眠等待下次刷新
            enterSleep();
            break;
        }

        case STATE_SLEEPING:
            // 不应该到达这里，因为 deep sleep 会重启
            delay(1000);
            break;

        case STATE_ERROR:
            delay(5000);
            ESP.restart();
            break;

        default:
            currentState = STATE_INIT;
            break;
    }
}
