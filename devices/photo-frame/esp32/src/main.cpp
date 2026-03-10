/**
 * @file main.cpp
 * @brief ESP32 墨水屏相框主程序
 *
 * 简化设计：只请求一个接口获取照片 bin 文件并显示
 */

#include <Arduino.h>
#include "config.h"
#include "wifi_manager.h"
#include "api_client.h"
#include "display_driver.h"

// 组件实例
WiFiManager wifiManager;
ApiClient apiClient;
DisplayDriver display;

// 状态机
enum State {
    STATE_INIT,
    STATE_CONNECT_WIFI,
    STATE_FETCH_DISPLAY,
    STATE_SHOW_DISPLAY,
    STATE_SLEEP,
    STATE_ERROR
};

State currentState = STATE_INIT;
State lastErrorState = STATE_INIT;
uint32_t errorCount = 0;
static const uint32_t MAX_ERROR_RETRY = 3;

// 定时器
uint32_t lastRefreshTime = 0;

// 函数声明
void enterState(State newState);
void handleInit();
void handleConnectWiFi();
void handleFetchDisplay();
void handleShowDisplay();
void handleSleep();
void handleError();
void blinkLED(uint32_t count, uint32_t onTime = 100, uint32_t offTime = 100);

void setup() {
    // 初始化串口
    Serial.begin(SERIAL_BAUD_RATE);
    delay(1000); // 等待串口稳定

#if LOG_LEVEL >= 3
    Serial.println("\n========================================");
    Serial.println("  Relive Photo Frame - ESP32");
    Serial.println("  Version: 1.0.0");
    Serial.println("========================================\n");
#endif

    // 初始化 LED
    pinMode(LED_PIN, OUTPUT);
    digitalWrite(LED_PIN, LOW);

    // 初始化按键
    pinMode(BUTTON_PIN, INPUT_PULLUP);

    // 初始化组件
    wifiManager.begin();
    apiClient.setBaseUrl(API_BASE_URL);
    apiClient.setApiKey(DEVICE_API_KEY);

    // 初始化显示（根据实际屏幕修改类型）
    if (!display.init(DISPLAY_GDEP073E01, EPD_PIN_CS, EPD_PIN_DC, EPD_PIN_RST, EPD_PIN_BUSY)) {
#if LOG_LEVEL >= 1
        Serial.println("[Main] Display init failed!");
#endif
        enterState(STATE_ERROR);
        return;
    }

    enterState(STATE_INIT);
}

void loop() {
    // 检查手动刷新按钮
    if (digitalRead(BUTTON_PIN) == LOW) {
        delay(50); // 消抖
        if (digitalRead(BUTTON_PIN) == LOW) {
#if LOG_LEVEL >= 3
            Serial.println("[Main] Button pressed, manual refresh");
#endif
            enterState(STATE_FETCH_DISPLAY);
            while (digitalRead(BUTTON_PIN) == LOW) {
                delay(10);
            }
        }
    }

    // 状态机处理
    switch (currentState) {
    case STATE_INIT:
        handleInit();
        break;
    case STATE_CONNECT_WIFI:
        handleConnectWiFi();
        break;
    case STATE_FETCH_DISPLAY:
        handleFetchDisplay();
        break;
    case STATE_SHOW_DISPLAY:
        handleShowDisplay();
        break;
    case STATE_SLEEP:
        handleSleep();
        break;
    case STATE_ERROR:
        handleError();
        break;
    }

    delay(100);
}

void enterState(State newState) {
    if (currentState != newState) {
#if LOG_LEVEL >= 4
        Serial.print("[State] ");
        Serial.print(currentState);
        Serial.print(" -> ");
        Serial.println(newState);
#endif
        currentState = newState;
    }
}

void handleInit() {
#if LOG_LEVEL >= 3
    Serial.println("[State] Initializing...");
#endif

    // 首次运行时显示启动画面或清空屏幕
    display.clear(EPD_WHITE);
    display.waitUntilIdle();

    enterState(STATE_CONNECT_WIFI);
}

void handleConnectWiFi() {
#if LOG_LEVEL >= 3
    Serial.println("[State] Connecting to WiFi...");
#endif

    blinkLED(1, 200, 200);

    if (wifiManager.connect(WIFI_SSID, WIFI_PASSWORD, WIFI_CONNECT_TIMEOUT_MS)) {
        errorCount = 0;
        enterState(STATE_FETCH_DISPLAY);
    } else {
        errorCount++;
#if LOG_LEVEL >= 1
        Serial.print("[State] WiFi connect failed, retry ");
        Serial.print(errorCount);
        Serial.print("/");
        Serial.println(MAX_ERROR_RETRY);
#endif
        if (errorCount >= MAX_ERROR_RETRY) {
            lastErrorState = STATE_CONNECT_WIFI;
            enterState(STATE_ERROR);
        } else {
            delay(5000); // 5秒后重试
        }
    }
}

void handleFetchDisplay() {
#if LOG_LEVEL >= 3
    Serial.println("[State] Fetching display data...");
#endif

    blinkLED(2, 100, 100);

    BinFileData binData;
    if (apiClient.getDisplayBin(binData)) {
        errorCount = 0;

#if LOG_LEVEL >= 3
        Serial.print("[State] Got bin: AssetID=");
        Serial.print(binData.header.assetId);
        Serial.print(", PhotoID=");
        Serial.print(binData.header.photoId);
        Serial.print(", Size=");
        Serial.print(binData.size);
        Serial.println(" bytes");
#endif

        // 显示数据
        if (display.displayBin(binData)) {
            // 等待刷新开始
            delay(100);
        }

        // 释放内存
        apiClient.freeBinData(binData);

        enterState(STATE_SHOW_DISPLAY);
    } else {
        errorCount++;
#if LOG_LEVEL >= 1
        Serial.print("[State] Fetch failed: ");
        Serial.println(apiClient.getLastError());
        Serial.print("[State] Retry ");
        Serial.print(errorCount);
        Serial.print("/");
        Serial.println(MAX_ERROR_RETRY);
#endif
        if (errorCount >= MAX_ERROR_RETRY) {
            lastErrorState = STATE_FETCH_DISPLAY;
            enterState(STATE_ERROR);
        } else {
            delay(5000);
        }
    }
}

void handleShowDisplay() {
    // 等待显示刷新完成
    if (display.isBusy()) {
        // 刷新中，继续等待
        blinkLED(1, 50, 1950); // 缓慢闪烁表示刷新中
        return;
    }

#if LOG_LEVEL >= 3
    Serial.println("[State] Display refresh complete");
#endif

    // 记录刷新时间
    lastRefreshTime = millis();

    // 断开 WiFi 省电
    wifiManager.disconnect();

    enterState(STATE_SLEEP);
}

void handleSleep() {
#if LOG_LEVEL >= 3
    Serial.println("[State] Entering sleep mode...");
#endif

    // 关闭显示（进入低功耗）
    display.sleep();

#if ENABLE_DEEP_SLEEP
    // 计算下次唤醒时间
    uint64_t sleepTimeUs = REFRESH_INTERVAL_DEEP_SLEEP;

#if LOG_LEVEL >= 3
    Serial.print("[State] Deep sleep for ");
    Serial.print(sleepTimeUs / 1000000);
    Serial.println(" seconds");
    Serial.println("[State] Good night!");
    Serial.flush();
#endif

    // 进入深度睡眠
    esp_sleep_enable_timer_wakeup(sleepTimeUs);
    esp_deep_sleep_start();
#else
    // 轻度睡眠模式（用于调试）
#if LOG_LEVEL >= 3
    Serial.println("[State] Light sleep mode (debug)");
#endif

    uint32_t sleepStart = millis();
    while (millis() - sleepStart < REFRESH_INTERVAL_MS) {
        // 可以在这里检查按键唤醒
        if (digitalRead(BUTTON_PIN) == LOW) {
            enterState(STATE_FETCH_DISPLAY);
            return;
        }
        delay(100);
    }

    // 唤醒显示
    display.wakeup();
    enterState(STATE_CONNECT_WIFI);
#endif
}

void handleError() {
#if LOG_LEVEL >= 1
    Serial.println("[State] ERROR state");
    Serial.print("[State] Last error state: ");
    Serial.println(lastErrorState);
#endif

    // 错误指示：快速闪烁
    blinkLED(5, 200, 200);

    // 延迟后重启
    delay(10000);

#if LOG_LEVEL >= 1
    Serial.println("[State] Restarting...");
#endif

    ESP.restart();
}

void blinkLED(uint32_t count, uint32_t onTime, uint32_t offTime) {
    for (uint32_t i = 0; i < count; i++) {
        digitalWrite(LED_PIN, HIGH);
        delay(onTime);
        digitalWrite(LED_PIN, LOW);
        delay(offTime);
    }
}
