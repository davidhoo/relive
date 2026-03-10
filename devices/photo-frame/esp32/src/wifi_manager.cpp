#include "wifi_manager.h"
#include <esp_wifi.h>

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

WiFiManager::WiFiManager() : _connected(false), _usingCustomMAC(false), _lastReconnectAttempt(0) {}

// 将字符串 MAC 地址解析为字节数组
// 支持格式: "AA:BB:CC:DD:EE:FF" 或 "AA-BB-CC-DD-EE-FF"
bool parseMACString(const char* macStr, uint8_t* macBytes) {
    if (macStr == nullptr || strlen(macStr) < 17) {
        return false;
    }

    unsigned int values[6];
    char separator = macStr[2]; // 获取分隔符 (: 或 -)

    if (separator == ':') {
        int matched = sscanf(macStr, "%02x:%02x:%02x:%02x:%02x:%02x",
                            &values[0], &values[1], &values[2],
                            &values[3], &values[4], &values[5]);
        if (matched != 6) return false;
    } else if (separator == '-') {
        int matched = sscanf(macStr, "%02x-%02x-%02x-%02x-%02x-%02x",
                            &values[0], &values[1], &values[2],
                            &values[3], &values[4], &values[5]);
        if (matched != 6) return false;
    } else {
        return false;
    }

    for (int i = 0; i < 6; i++) {
        macBytes[i] = (uint8_t)values[i];
    }
    return true;
}

bool WiFiManager::setupCustomMAC() {
#ifdef USE_CUSTOM_MAC_ADDRESS
    uint8_t customMAC[6] = {0};
    bool valid = false;

#ifdef CUSTOM_MAC_ADDRESS_STRING
    // 字符串格式: "AA:BB:CC:DD:EE:FF"
    valid = parseMACString(CUSTOM_MAC_ADDRESS_STRING, customMAC);
    if (!valid) {
        DEBUG_SERIAL.println("[WiFi] 自定义 MAC 地址格式无效，使用默认 MAC");
        _usingCustomMAC = false;
        return false;
    }
#elif defined(CUSTOM_MAC_ADDRESS)
    // 数组格式: {0x14, 0x2B, 0x2F, 0xEC, 0x0B, 0x04}
    uint8_t macArray[] = CUSTOM_MAC_ADDRESS;
    memcpy(customMAC, macArray, 6);
    valid = true;
#else
    DEBUG_SERIAL.println("[WiFi] 未定义 CUSTOM_MAC_ADDRESS_STRING 或 CUSTOM_MAC_ADDRESS，使用默认 MAC");
    _usingCustomMAC = false;
    return false;
#endif

    if (valid) {
        // 检查是否是有效的 MAC 地址（非全零）
        bool isNonZero = false;
        for (int i = 0; i < 6; i++) {
            if (customMAC[i] != 0x00) {
                isNonZero = true;
                break;
            }
        }

        if (!isNonZero) {
            DEBUG_SERIAL.println("[WiFi] 自定义 MAC 地址无效（全零），使用默认 MAC");
            _usingCustomMAC = false;
            return false;
        }

        // 确保 MAC 地址符合单播地址要求（第一个字节的最低位为0）
        // 并设置为本地管理地址（第一个字节的次低位为1）
        customMAC[0] &= 0xFE; // 清除组播位（bit 0），确保是单播
        customMAC[0] |= 0x02; // 设置本地管理位（bit 1）

        DEBUG_SERIAL.printf("[WiFi] 准备设置 MAC: %02X:%02X:%02X:%02X:%02X:%02X\n",
                           customMAC[0], customMAC[1], customMAC[2],
                           customMAC[3], customMAC[4], customMAC[5]);

        // 确保 WiFi 已初始化并处于 STA 模式
        if (WiFi.getMode() == WIFI_OFF) {
            WiFi.mode(WIFI_STA);
            delay(100); // 给时间让 WiFi 初始化
        }

        // 必须先停止 WiFi 才能设置 MAC 地址
        esp_err_t stopResult = esp_wifi_stop();
        if (stopResult != ESP_OK) {
            DEBUG_SERIAL.printf("[WiFi] 停止 WiFi 失败，错误码: %d\n", stopResult);
        }
        delay(100);

        // 设置自定义 MAC 地址
        esp_err_t result = esp_wifi_set_mac(WIFI_IF_STA, customMAC);

        if (result == ESP_OK) {
            _usingCustomMAC = true;
            DEBUG_SERIAL.printf("[WiFi] 已设置自定义 MAC: %02X:%02X:%02X:%02X:%02X:%02X\n",
                               customMAC[0], customMAC[1], customMAC[2],
                               customMAC[3], customMAC[4], customMAC[5]);
            return true;
        } else {
            DEBUG_SERIAL.printf("[WiFi] 设置自定义 MAC 失败，错误码: %d\n", result);
            _usingCustomMAC = false;
            return false;
        }
    }

    _usingCustomMAC = false;
    return false;
#else
    _usingCustomMAC = false;
    return false;
#endif
}

bool WiFiManager::isUsingCustomMAC() {
    return _usingCustomMAC;
}

bool WiFiManager::begin() {
    DEBUG_SERIAL.println("[WiFi] 初始化...");

    // 必须先设置 WiFi 模式
    WiFi.mode(WIFI_STA);
    delay(100);

    // 在 WiFi.begin() 之前设置自定义 MAC 地址
    setupCustomMAC();

    // 设置 MAC 后需要重新启动 WiFi
    esp_wifi_start();
    delay(100);

    WiFi.begin(WIFI_SSID, WIFI_PASSWORD);

    DEBUG_SERIAL.printf("[WiFi] 连接到: %s\n", WIFI_SSID);

    // 等待连接，最多 30 秒
    int attempts = 0;
    while (WiFi.status() != WL_CONNECTED && attempts < 60) {
        delay(500);
        DEBUG_SERIAL.print(".");
        attempts++;
    }
    DEBUG_SERIAL.println();

    if (WiFi.status() == WL_CONNECTED) {
        _connected = true;
        DEBUG_SERIAL.println("[WiFi] 连接成功!");
        DEBUG_SERIAL.printf("[WiFi] IP 地址: %s\n", WiFi.localIP().toString().c_str());
        DEBUG_SERIAL.printf("[WiFi] MAC 地址: %s %s\n", WiFi.macAddress().c_str(),
                           _usingCustomMAC ? "(自定义)" : "(默认)");
        DEBUG_SERIAL.printf("[WiFi] 信号强度: %d dBm\n", WiFi.RSSI());
        return true;
    } else {
        _connected = false;
        DEBUG_SERIAL.println("[WiFi] 连接失败!");
        return false;
    }
}

bool WiFiManager::isConnected() {
    _connected = (WiFi.status() == WL_CONNECTED);
    return _connected;
}

String WiFiManager::getLocalIP() {
    if (isConnected()) {
        return WiFi.localIP().toString();
    }
    return String("0.0.0.0");
}

String WiFiManager::getMACAddress() {
    return WiFi.macAddress();
}

void WiFiManager::disconnect() {
    WiFi.disconnect();
    _connected = false;
    DEBUG_SERIAL.println("[WiFi] 已断开连接");
}

bool WiFiManager::reconnect() {
    unsigned long currentMillis = millis();

    // 检查是否到了重连间隔
    if (currentMillis - _lastReconnectAttempt < RECONNECT_INTERVAL) {
        return false;
    }

    _lastReconnectAttempt = currentMillis;

    DEBUG_SERIAL.println("[WiFi] 尝试重新连接...");

    WiFi.disconnect();
    delay(1000);

    // 重新设置自定义 MAC 地址（如果启用了）
    if (_usingCustomMAC) {
        setupCustomMAC();
        esp_wifi_start();
        delay(100);
    }

    WiFi.begin(WIFI_SSID, WIFI_PASSWORD);

    int attempts = 0;
    while (WiFi.status() != WL_CONNECTED && attempts < 30) {
        delay(500);
        DEBUG_SERIAL.print(".");
        attempts++;
    }
    DEBUG_SERIAL.println();

    if (WiFi.status() == WL_CONNECTED) {
        _connected = true;
        DEBUG_SERIAL.println("[WiFi] 重连成功!");
        DEBUG_SERIAL.printf("[WiFi] IP 地址: %s\n", WiFi.localIP().toString().c_str());
        DEBUG_SERIAL.printf("[WiFi] MAC 地址: %s %s\n", WiFi.macAddress().c_str(),
                           _usingCustomMAC ? "(自定义)" : "(默认)");
        return true;
    } else {
        DEBUG_SERIAL.println("[WiFi] 重连失败!");
        return false;
    }
}
