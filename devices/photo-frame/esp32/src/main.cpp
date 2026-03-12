#include <Arduino.h>
#include <HTTPClient.h>
#include <WiFiClientSecure.h>
#include <driver/gpio.h>
#include <esp_sleep.h>
#include "config.h"
#include "log.h"
#include "wifi_manager.h"
#include "api_client.h"
#include "display_driver.h"
#include "nvs_config.h"
#include "schedule_manager.h"
#include "web_portal.h"

// 全局对象
WiFiManager wifiManager;
APIClient apiClient;
DisplayDriver display;
NVSConfig nvsConfig;
ScheduleManager scheduleManager;
WebPortal webPortal;

// 图像缓冲区
uint8_t* imageBuffer = nullptr;
const size_t BUFFER_SIZE = 200000;  // 192000 + 余量
size_t actualBufferSize = 0;

// 统计信息
struct {
    int successCount = 0;
    int errorCount = 0;
} stats;

// ===================== 辅助函数 =====================

// 深睡前统一关闭外围设备，最小化待机电流
void prepareSleep() {
    // 1. 墨水屏进入深睡（DSLP）+ 关闭 SPI
    display.sleep();

    // 2. 彻底关闭 WiFi radio
    wifiManager.disconnect();

    // 3. 关闭 NVS
    // NVS Preferences 在 begin() 时打开，睡前关闭释放 flash 控制器
    // (NVSConfig::end() 不存在，直接用底层 API 无影响，深睡会断电)

    // 4. 隔离 GPIO 引脚，防止 SPI 引脚悬浮漏电到墨水屏控制器
    gpio_hold_en((gpio_num_t)EINK_RST);
    gpio_hold_en((gpio_num_t)EINK_DC);
    gpio_hold_en((gpio_num_t)EINK_CS);
    gpio_hold_en((gpio_num_t)EINK_MOSI);
    gpio_hold_en((gpio_num_t)EINK_SCK);
    gpio_deep_sleep_hold_en();

    // 5. 关闭不需要的 RTC 电源域
    esp_sleep_pd_config(ESP_PD_DOMAIN_RC_FAST, ESP_PD_OPTION_OFF);

    // 6. 刷新串口缓冲
    DEBUG_SERIAL.flush();
}

void showStartupScreen() {
    DEBUG_SERIAL.println("\n=================================");
    DEBUG_SERIAL.println("   Relive 智能相框 v2");
    DEBUG_SERIAL.println("   ESP32-S3 + E Ink Spectra 6");
    DEBUG_SERIAL.println("=================================\n");
}

bool allocateBuffer() {
    if (imageBuffer != nullptr) return true;

    LOG_DEBUG("[System] 内存分配...");
    LOG_DEBUG_F("[System] 堆: %d, 空闲: %d, 最大块: %d\n",
                ESP.getHeapSize(), ESP.getFreeHeap(), ESP.getMaxAllocHeap());

    if (psramFound()) {
        LOG_DEBUG_F("[System] PSRAM: %d / %d\n", ESP.getFreePsram(), ESP.getPsramSize());
        actualBufferSize = BUFFER_SIZE;
        imageBuffer = (uint8_t*)ps_malloc(actualBufferSize);
        if (imageBuffer) {
            LOG_INFO_F("[System] PSRAM 分配成功: %d bytes\n", actualBufferSize);
            return true;
        }
    }

    actualBufferSize = BUFFER_SIZE;
    if (ESP.getMaxAllocHeap() < actualBufferSize) {
        LOG_ERROR("[System] 内存不足，需要 PSRAM");
        return false;
    }

    imageBuffer = (uint8_t*)malloc(actualBufferSize);
    if (imageBuffer) {
        LOG_INFO_F("[System] 堆内存分配成功: %d bytes\n", actualBufferSize);
        return true;
    }

    LOG_ERROR("[System] 内存分配失败");
    return false;
}

// ===================== AP 配网流程 =====================

void runAPPortal() {
    LOG_INFO("[Main] 进入 AP 配网模式");

    wifiManager.startAP();

    // 显示配网引导
    display.showAPGuide(AP_SSID, "http://192.168.4.1");

    // 启动 Web 配置页面
    webPortal.begin(&wifiManager, &nvsConfig);

    unsigned long startTime = millis();
    bool clientConnected = false;

    while (millis() - startTime < AP_TIMEOUT_MS) {
        webPortal.handleClient();

        // 检查是否有设备连接到 AP
        if (WiFi.softAPgetStationNum() > 0) {
            clientConnected = true;
            startTime = millis(); // 有设备连接则重置超时
        }

        // 用户已提交配置
        if (webPortal.isConfigured()) {
            LOG_INFO("[Main] 配置已保存，执行 NTP 同步后重启");
            // NTP 同步（AP 配网保存时的唯一主动 NTP 时机）
            // 需要先连接到用户配置的 WiFi
            wifiManager.stopAP();
            String ssid = nvsConfig.getWiFiSSID();
            String pass = nvsConfig.getWiFiPass();
            if (wifiManager.connectWithCredentials(ssid, pass)) {
                scheduleManager.syncNTP();
            }
            delay(1000);
            ESP.restart();
            return;
        }

        delay(10);
    }

    // AP 超时
    webPortal.stop();
    wifiManager.stopAP();

    // 如果 NVS 有配置，尝试已有配置连接（容错路由器临时掉电）
    if (nvsConfig.isConfigured()) {
        LOG_INFO("[Main] AP 超时，尝试已有 NVS 配置连接...");
        String ssid = nvsConfig.getWiFiSSID();
        String pass = nvsConfig.getWiFiPass();
        if (wifiManager.connectWithCredentials(ssid, pass)) {
            LOG_INFO("[Main] NVS 配置连接成功，继续正常流程");
            nvsConfig.resetAPFailCount();
            return; // 返回到正常流程
        }
    }

    // 退避睡眠
    uint8_t failCount = nvsConfig.getAPFailCount();
    const int backoffMinutes[] = AP_BACKOFF_MINUTES;
    int sleepMinutes = backoffMinutes[min((int)failCount, AP_BACKOFF_STEPS - 1)];

    nvsConfig.setAPFailCount(failCount + 1);

    char msg[128];
    snprintf(msg, sizeof(msg),
             "WiFi not configured / connection failed.\n"
             "Retrying in %d minutes...", sleepMinutes);
    display.showSleepMessage(msg);

    LOG_INFO_F("[Main] 退避睡眠 %d 分钟 (fail count: %d)\n", sleepMinutes, failCount + 1);

    prepareSleep();
    uint64_t sleepUs = (uint64_t)sleepMinutes * 60ULL * 1000000ULL;
    esp_sleep_enable_timer_wakeup(sleepUs);
    esp_deep_sleep_start();
}

// ===================== 正常工作流程 =====================

bool downloadAndDisplay() {
    LOG_INFO("[Main] 开始下载照片...");

    if (!wifiManager.isConnected()) {
        LOG_ERROR("[Main] WiFi 未连接");
        return false;
    }

    String receivedChecksum;
    int downloaded = apiClient.downloadBinFile(imageBuffer, actualBufferSize, receivedChecksum);

    if (downloaded <= 0) {
        LOG_ERROR_F("[Main] 下载失败: %s\n", apiClient.getLastError().c_str());
        stats.errorCount++;
        return false;
    }

    // 校准 RTC（如果收到 X-Server-Time）
    long serverTime = apiClient.getLastServerTime();
    if (serverTime > 0) {
        scheduleManager.syncTimeFromServer(serverTime);
    }

    LOG_INFO_F("[Main] 下载成功: %d bytes\n", downloaded);

    display.display(imageBuffer, downloaded);

    stats.successCount++;
    LOG_INFO("[Main] 显示完成");
    return true;
}

void enterSmartSleep() {
    LOG_INFO("[Main] 准备睡眠...");

    uint64_t sleepMs = scheduleManager.calculateSleepDurationMs();

    prepareSleep();

    uint64_t sleepUs = sleepMs * 1000ULL;
    esp_sleep_enable_timer_wakeup(sleepUs);

    LOG_INFO_F("[Main] 深度睡眠 %llu 秒\n", sleepMs / 1000);
    esp_deep_sleep_start();
}

// ===================== 主状态机 =====================

void setup() {
    DEBUG_SERIAL.begin(DEBUG_BAUDRATE);
    delay(1000);

    showStartupScreen();

    // 检查唤醒原因
    esp_sleep_wakeup_cause_t wakeup_reason = esp_sleep_get_wakeup_cause();
    if (wakeup_reason == ESP_SLEEP_WAKEUP_TIMER) {
        LOG_INFO("[Main] 从定时器唤醒");
        // 释放深睡期间的 GPIO hold，让引脚可以重新配置
        gpio_hold_dis((gpio_num_t)EINK_RST);
        gpio_hold_dis((gpio_num_t)EINK_DC);
        gpio_hold_dis((gpio_num_t)EINK_CS);
        gpio_hold_dis((gpio_num_t)EINK_MOSI);
        gpio_hold_dis((gpio_num_t)EINK_SCK);
        gpio_deep_sleep_hold_dis();
    } else {
        LOG_INFO("[Main] 正常启动/复位");
    }

    // 初始化 NVS 配置
    nvsConfig.begin();

    // 分配缓冲区
    if (!allocateBuffer()) {
        LOG_ERROR("[Main] 内存分配失败，重启");
        delay(5000);
        ESP.restart();
        return;
    }

    // 初始化显示
    if (!display.begin()) {
        LOG_ERROR("[Main] 显示初始化失败，重启");
        delay(5000);
        ESP.restart();
        return;
    }

    // ===== WiFi 扫描 + 模式判断 =====

    if (wifiManager.begin()) {
        // 办公室模式：编译时凭据连接成功
        LOG_INFO("[Main] 办公室模式，使用编译时配置");
        apiClient.begin();

        // 加载刷新计划
        String schedules = DEFAULT_SCHEDULES;
        scheduleManager.parseSchedules(schedules);

        // 重置 AP 失败计数
        nvsConfig.resetAPFailCount();
    } else {
        // 非办公室模式
        if (!nvsConfig.isConfigured()) {
            // NVS 未配置 → AP 配网
            LOG_INFO("[Main] NVS 未配置，进入 AP 配网");
            runAPPortal();
            // 如果 runAPPortal 返回（NVS 已连接成功），继续下面的流程
            if (!wifiManager.isConnected()) {
                return; // 已进入深度睡眠，不会到这里
            }
            // 连接成功后设置 API 客户端
            apiClient.beginWithConfig(
                nvsConfig.getServerHost(),
                nvsConfig.getServerPort(),
                nvsConfig.getAPIKey()
            );
            String schedules = nvsConfig.getSchedules();
            if (schedules.length() == 0) schedules = DEFAULT_SCHEDULES;
            scheduleManager.parseSchedules(schedules);
        } else {
            // NVS 已配置 → 尝试连接
            LOG_INFO("[Main] 使用 NVS 配置连接");
            String ssid = nvsConfig.getWiFiSSID();
            String pass = nvsConfig.getWiFiPass();

            int retries = 0;
            bool connected = false;
            while (retries < MAX_WIFI_RETRIES) {
                if (wifiManager.connectWithCredentials(ssid, pass)) {
                    connected = true;
                    break;
                }
                retries++;
                LOG_INFO_F("[Main] WiFi 重试 %d/%d\n", retries, MAX_WIFI_RETRIES);
                delay(RETRY_DELAY_MS);
            }

            if (!connected) {
                // 连续失败 N 次 → AP 配网
                LOG_ERROR("[Main] WiFi 连续失败，进入 AP 配网");
                runAPPortal();
                if (!wifiManager.isConnected()) {
                    return;
                }
            }

            // 成功连接
            nvsConfig.resetAPFailCount();
            apiClient.beginWithConfig(
                nvsConfig.getServerHost(),
                nvsConfig.getServerPort(),
                nvsConfig.getAPIKey()
            );
            String schedules = nvsConfig.getSchedules();
            if (schedules.length() == 0) schedules = DEFAULT_SCHEDULES;
            scheduleManager.parseSchedules(schedules);
        }
    }

    // ===== 正常工作流程 =====

    // 时间无效时尝试 NTP 同步
    if (!scheduleManager.isTimeValid()) {
        LOG_INFO("[Main] 时间无效，尝试 NTP 同步...");
        scheduleManager.syncNTP();
    }

    // 下载并显示照片
    int retryCount = 0;
    bool success = false;
    while (retryCount < MAX_RETRY_COUNT) {
        if (downloadAndDisplay()) {
            success = true;
            break;
        }
        retryCount++;
        LOG_INFO_F("[Main] 下载重试 %d/%d\n", retryCount, MAX_RETRY_COUNT);
        delay(RETRY_DELAY_MS);
    }

    if (!success) {
        LOG_ERROR("[Main] 下载失败，进入睡眠等待下次重试");
    }

    LOG_INFO_F("[Main] 成功: %d, 失败: %d\n", stats.successCount, stats.errorCount);

    // 智能睡眠
    enterSmartSleep();
}

void loop() {
    // 正常情况下不会执行到这里（setup 结束后进入深度睡眠）
    // 仅作为安全兜底
    delay(1000);
    LOG_ERROR("[Main] 意外进入 loop，重启");
    ESP.restart();
}
