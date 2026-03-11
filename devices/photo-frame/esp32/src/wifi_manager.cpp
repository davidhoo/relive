#include "wifi_manager.h"
#include <esp_wifi.h>
#include <esp_mac.h>

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

// 解析自定义 MAC 配置到 _customMAC，返回是否有效
bool WiFiManager::parseCustomMAC() {
#ifdef USE_CUSTOM_MAC_ADDRESS
#ifdef CUSTOM_MAC_ADDRESS_STRING
    // 字符串格式: "AA:BB:CC:DD:EE:FF"
    if (!parseMACString(CUSTOM_MAC_ADDRESS_STRING, _customMAC)) {
        DEBUG_SERIAL.println("[WiFi] 自定义 MAC 地址格式无效，使用默认 MAC");
        return false;
    }
#elif defined(CUSTOM_MAC_ADDRESS)
    // 数组格式: {0x14, 0x2B, 0x2F, 0xEC, 0x0B, 0x04}
    uint8_t macArray[] = CUSTOM_MAC_ADDRESS;
    memcpy(_customMAC, macArray, 6);
#else
    DEBUG_SERIAL.println("[WiFi] 未定义 CUSTOM_MAC_ADDRESS_STRING 或 CUSTOM_MAC_ADDRESS，使用默认 MAC");
    return false;
#endif

    // 检查是否是有效的 MAC 地址（非全零）
    bool isNonZero = false;
    for (int i = 0; i < 6; i++) {
        if (_customMAC[i] != 0x00) {
            isNonZero = true;
            break;
        }
    }
    if (!isNonZero) {
        DEBUG_SERIAL.println("[WiFi] 自定义 MAC 地址无效（全零），使用默认 MAC");
        return false;
    }

    return true;
#else
    return false;
#endif
}

// 在 WiFi 初始化之前设置系统级 base MAC
// ESP32 的 STA MAC = base MAC，所以直接设置 base MAC 即可
bool WiFiManager::applyBaseMAC() {
    DEBUG_SERIAL.printf("[WiFi] 设置系统 base MAC: %02X:%02X:%02X:%02X:%02X:%02X\n",
                       _customMAC[0], _customMAC[1], _customMAC[2],
                       _customMAC[3], _customMAC[4], _customMAC[5]);

    // esp_base_mac_addr_set 必须在 esp_wifi_init (即 WiFi.mode) 之前调用
    // STA MAC = base MAC，无需额外偏移
    esp_err_t result = esp_base_mac_addr_set(_customMAC);
    if (result != ESP_OK) {
        DEBUG_SERIAL.printf("[WiFi] 设置 base MAC 失败，错误码: %d\n", result);
        return false;
    }

    DEBUG_SERIAL.println("[WiFi] 系统 base MAC 设置成功");
    return true;
}

// 验证当前 WiFi 接口实际使用的 MAC 地址
void WiFiManager::verifyMAC() {
    uint8_t actualMAC[6] = {0};
    esp_wifi_get_mac(WIFI_IF_STA, actualMAC);

    DEBUG_SERIAL.printf("[WiFi] 实际 MAC: %02X:%02X:%02X:%02X:%02X:%02X\n",
                       actualMAC[0], actualMAC[1], actualMAC[2],
                       actualMAC[3], actualMAC[4], actualMAC[5]);

    if (_usingCustomMAC) {
        bool match = (memcmp(actualMAC, _customMAC, 6) == 0);
        DEBUG_SERIAL.printf("[WiFi] MAC 验证: %s\n", match ? "一致 ✓" : "不一致 ✗");
        if (!match) {
            DEBUG_SERIAL.printf("[WiFi] 期望 MAC: %02X:%02X:%02X:%02X:%02X:%02X\n",
                               _customMAC[0], _customMAC[1], _customMAC[2],
                               _customMAC[3], _customMAC[4], _customMAC[5]);
        }
    }
}

bool WiFiManager::isUsingCustomMAC() {
    return _usingCustomMAC;
}

bool WiFiManager::begin() {
    DEBUG_SERIAL.println("[WiFi] 初始化...");

    // 解析自定义 MAC 配置
    _usingCustomMAC = parseCustomMAC();

    // 在 WiFi.mode() 之前设置 base MAC（关键！）
    // esp_base_mac_addr_set 必须在 WiFi 子系统初始化之前调用
    if (_usingCustomMAC) {
        if (!applyBaseMAC()) {
            _usingCustomMAC = false;
        }
    }

    // 初始化 WiFi 为 STA 模式（此时会使用已设置的 base MAC）
    WiFi.mode(WIFI_STA);
    delay(100);

    // 连接 WiFi
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

        // 用底层 API 验证实际 MAC
        verifyMAC();

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

    // base MAC 已在 begin() 中设置，无需重复设置
    // 它在系统级生效，直到下次重启

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
        verifyMAC();
        return true;
    } else {
        DEBUG_SERIAL.println("[WiFi] 重连失败!");
        return false;
    }
}
