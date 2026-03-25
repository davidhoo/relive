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

// 电池电压（setup 中测量，全局保存）
float gBatteryVoltage = 0.0;

// 低电量阈值
#define BAT_LOW_VOLTAGE 3.0

// ===================== 辅助函数 =====================

// 测量电池电压：拉高 GPIO5 导通 NMOS 采样电路，ADC 多次采样取平均
float measureBatteryVoltage() {
    // 拉高 NMOS 栅极，导通 ADC 采样电路
    pinMode(BAT_ADC_EN, OUTPUT);
    digitalWrite(BAT_ADC_EN, HIGH);
    delay(10);  // 等待电路稳定

    // 配置 ADC 引脚
    analogReadResolution(12);  // 12-bit: 0-4095
    analogSetAttenuation(ADC_11db);  // 满量程 ~3.3V

    // 多次采样取平均
    uint32_t sum = 0;
    for (int i = 0; i < BAT_ADC_SAMPLES; i++) {
        sum += analogRead(BAT_ADC_PIN);
        delay(10);
    }
    float avgRaw = (float)sum / BAT_ADC_SAMPLES;

    // 关断 NMOS，断开采样电路
    digitalWrite(BAT_ADC_EN, LOW);

    // 计算实际电压：ADC 电压 × 分压比
    float adcVoltage = avgRaw / 4095.0 * 3.3;
    float batteryVoltage = adcVoltage * BAT_DIVIDER_RATIO;

    LOG_INFO_F("[Battery] ADC raw avg: %.0f, ADC voltage: %.2fV, Battery: %.2fV\n",
               avgRaw, adcVoltage, batteryVoltage);

    return batteryVoltage;
}

// 在 4-bit 缓冲区中设置单个像素颜色
// buffer: 4-bit packed (每字节2像素，高4位=偶数像素，低4位=奇数像素)
static inline void setPixel4bit(uint8_t* buffer, int x, int y, uint8_t color) {
    int idx = y * (SCREEN_WIDTH / 2) + x / 2;
    if (x % 2 == 0) {
        buffer[idx] = (color << 4) | (buffer[idx] & 0x0F);
    } else {
        buffer[idx] = (buffer[idx] & 0xF0) | (color & 0x0F);
    }
}

// 在竖屏坐标系中设置像素，自动转换到 buffer 坐标系
// 服务端将竖屏 canvas(480×800) 逆时针旋转 90° 输出 buffer(800×480)
// 映射关系: portrait(px, py) → buffer(799-py, px)
static inline void setPixelPortrait(uint8_t* buffer, int px, int py, uint8_t color) {
    int bx = (SCREEN_WIDTH - 1) - py;
    int by = px;
    setPixel4bit(buffer, bx, by, color);
}

// 在竖屏视角右上角绘制横向红色低电量电池图标，正极在右侧
static void drawLowBatteryIcon(uint8_t* buffer) {
    // 竖屏尺寸: 宽=SCREEN_HEIGHT(480), 高=SCREEN_WIDTH(800)
    const int portraitW = SCREEN_HEIGHT;  // 480
    const int margin = 10;

    const int bodyW = 44;    // 电池主体宽度
    const int bodyH = 22;    // 电池主体高度
    const int nubW  = 4;     // 正极凸起宽度
    const int nubH  = 10;    // 正极凸起高度
    const int bw    = 2;     // 边框粗细
    const int r     = 3;     // 圆角半径

    // 竖屏右上角定位
    int x0 = portraitW - margin - bodyW - nubW;
    int y0 = margin;

    const uint8_t cBlack = EINK_BLACK;
    const uint8_t cRed   = EINK_RED;
    const uint8_t cWhite = EINK_WHITE;

    // 判断像素是否在圆角矩形内部
    // 四个角用半径 r 的圆弧裁切，其余区域为普通矩形
    auto inRoundedRect = [&](int x, int y, int rx, int ry, int rw, int rh, int rad) -> bool {
        if (x < rx || x >= rx + rw || y < ry || y >= ry + rh) return false;
        // 检查四个角
        int cx, cy;
        if (x < rx + rad && y < ry + rad) {
            cx = rx + rad; cy = ry + rad;
        } else if (x >= rx + rw - rad && y < ry + rad) {
            cx = rx + rw - rad - 1; cy = ry + rad;
        } else if (x < rx + rad && y >= ry + rh - rad) {
            cx = rx + rad; cy = ry + rh - rad - 1;
        } else if (x >= rx + rw - rad && y >= ry + rh - rad) {
            cx = rx + rw - rad - 1; cy = ry + rh - rad - 1;
        } else {
            return true;  // 非角落区域，直接在内
        }
        int dx = x - cx, dy = y - cy;
        return (dx * dx + dy * dy) <= (rad * rad);
    };

    // 1. 电池主体：圆角矩形外框 + 白色内部
    for (int y = y0; y < y0 + bodyH; y++) {
        for (int x = x0; x < x0 + bodyW; x++) {
            if (!inRoundedRect(x, y, x0, y0, bodyW, bodyH, r)) continue;
            bool inner = inRoundedRect(x, y, x0 + bw, y0 + bw, bodyW - bw * 2, bodyH - bw * 2, r > bw ? r - bw : 0);
            setPixelPortrait(buffer, x, y, inner ? cWhite : cBlack);
        }
    }

    // 2. 内部红色填充（低电量，左侧约 1/4）
    int fillW = (bodyW - bw * 2) / 4;
    int innerR = r > bw ? r - bw : 0;
    for (int y = y0 + bw; y < y0 + bodyH - bw; y++) {
        for (int x = x0 + bw; x < x0 + bw + fillW; x++) {
            if (inRoundedRect(x, y, x0 + bw, y0 + bw, bodyW - bw * 2, bodyH - bw * 2, innerR)) {
                setPixelPortrait(buffer, x, y, cRed);
            }
        }
    }

    // 3. 正极凸起（黑色，右侧垂直居中，带右侧圆角）
    int nubX0 = x0 + bodyW;
    int nubY0 = y0 + (bodyH - nubH) / 2;
    int nubR = 2;  // 凸起右侧小圆角
    for (int y = nubY0; y < nubY0 + nubH; y++) {
        for (int x = nubX0; x < nubX0 + nubW; x++) {
            // 只对右侧两个角做圆角
            bool skip = false;
            if (x >= nubX0 + nubW - nubR && y < nubY0 + nubR) {
                int dx = x - (nubX0 + nubW - nubR - 1), dy = y - (nubY0 + nubR);
                if (dx * dx + dy * dy > nubR * nubR) skip = true;
            } else if (x >= nubX0 + nubW - nubR && y >= nubY0 + nubH - nubR) {
                int dx = x - (nubX0 + nubW - nubR - 1), dy = y - (nubY0 + nubH - nubR - 1);
                if (dx * dx + dy * dy > nubR * nubR) skip = true;
            }
            if (!skip) setPixelPortrait(buffer, x, y, cBlack);
        }
    }

    LOG_INFO("[Battery] 低电量图标已绘制");
}

// 深睡前统一关闭外围设备，最小化待机电流
void prepareSleep() {
    // 1. 墨水屏进入深睡（DSLP）+ 关闭 SPI
    display.sleep();

    // 2. 彻底关闭 WiFi radio
    wifiManager.disconnect();

    // 3. 确保 ADC 采样电路关断
    digitalWrite(BAT_ADC_EN, LOW);

    // 4. 隔离 GPIO 引脚，防止深睡期间引脚悬浮漏电
    gpio_hold_en((gpio_num_t)BAT_ADC_EN);   // 保持 LOW，防止 NMOS 栅极浮空导通
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

    // 低电量时在右上角叠加红色电池图标
    if (gBatteryVoltage > 0 && gBatteryVoltage < BAT_LOW_VOLTAGE) {
        drawLowBatteryIcon(imageBuffer);
    }

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

    // 测量电池电压（唤醒后第一时间采样）
    gBatteryVoltage = measureBatteryVoltage();

    // 检查唤醒原因
    esp_sleep_wakeup_cause_t wakeup_reason = esp_sleep_get_wakeup_cause();
    if (wakeup_reason == ESP_SLEEP_WAKEUP_TIMER) {
        LOG_INFO("[Main] 从定时器唤醒");
        // 释放深睡期间的 GPIO hold，让引脚可以重新配置
        gpio_hold_dis((gpio_num_t)BAT_ADC_EN);
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

    // 双击上电检测窗口计时
    unsigned long bootFlagSetTime = 0;

    // 双击上电检测：15 秒内两次上电 → 进入 AP 配网模式
    if (nvsConfig.getBootFlag()) {
        // boot_flag 为 true，说明上次启动未满 15 秒就断电了
        LOG_INFO("[Main] 检测到快速二次上电，进入 AP 配网模式");
        nvsConfig.setBootFlag(false);

        // 只初始化显示（AP 引导页需要），不分配 imageBuffer 以节省内存
        if (display.begin()) {
            runAPPortal();
            if (wifiManager.isConnected()) {
                apiClient.beginWithConfig(
                    nvsConfig.getServerHost(),
                    nvsConfig.getServerPort(),
                    nvsConfig.getAPIKey()
                );
                String schedules = nvsConfig.getSchedules();
                if (schedules.length() == 0) schedules = DEFAULT_SCHEDULES;
                scheduleManager.parseSchedules(schedules);
                // 从 AP 配网回来，分配 imageBuffer 后进入正常工作流程
                if (!allocateBuffer()) {
                    LOG_ERROR("[Main] 内存分配失败，重启");
                    delay(5000);
                    ESP.restart();
                    return;
                }
            } else {
                return; // AP 超时已进入深度睡眠
            }
        } else {
            LOG_ERROR("[Main] 初始化失败，重启");
            delay(5000);
            ESP.restart();
            return;
        }
    } else {
        // 正常启动：设置 boot_flag，15 秒后清零
        nvsConfig.setBootFlag(true);
        bootFlagSetTime = millis();

        // 分配缓冲区
        if (!allocateBuffer()) {
            LOG_ERROR("[Main] 内存分配失败，重启");
            nvsConfig.setBootFlag(false);
            delay(5000);
            ESP.restart();
            return;
        }

        // 初始化显示
        if (!display.begin()) {
            LOG_ERROR("[Main] 显示初始化失败，重启");
            nvsConfig.setBootFlag(false);
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
    }

    // ===== 正常工作流程 =====

    // 清除 boot_flag（确保 15 秒窗口已过）
    if (bootFlagSetTime > 0) {
        unsigned long elapsed = millis() - bootFlagSetTime;
        if (elapsed < 15000) {
            LOG_INFO_F("[Main] 等待双击检测窗口关闭（剩余 %lu ms）\n", 15000 - elapsed);
            delay(15000 - elapsed);
        }
        nvsConfig.setBootFlag(false);
    }

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
