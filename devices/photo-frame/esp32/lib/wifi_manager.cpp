/**
 * @file wifi_manager.cpp
 * @brief WiFi 连接管理器实现
 */

#include "wifi_manager.h"
#include "config.h"
#include <esp_wifi.h>

WiFiManager::WiFiManager() : _connected(false), _lastReconnectAttempt(0) {
}

void WiFiManager::begin() {
    WiFi.mode(WIFI_STA);
    WiFi.setAutoReconnect(true);
    WiFi.setSleep(false);

    // 设置 WiFi 功率（平衡功耗和信号强度）
    WiFi.setTxPower(WIFI_POWER_15dBm);

#if WIFI_USE_CUSTOM_MAC
    // 解析字符串格式的 MAC 地址 (AA:BB:CC:DD:EE:FF 或 AA-BB-CC-DD-EE-FF)
    uint8_t customMac[6];
    if (parseMacAddress(WIFI_CUSTOM_MAC, customMac)) {
        if (esp_wifi_set_mac(WIFI_IF_STA, customMac) == ESP_OK) {
#if LOG_LEVEL >= 3
            Serial.printf("[WiFi] Custom MAC set: %02X:%02X:%02X:%02X:%02X:%02X\n",
                          customMac[0], customMac[1], customMac[2],
                          customMac[3], customMac[4], customMac[5]);
#endif
        } else {
#if LOG_LEVEL >= 1
            Serial.println("[WiFi] Failed to set custom MAC!");
#endif
        }
    } else {
#if LOG_LEVEL >= 1
        Serial.println("[WiFi] Invalid MAC address format!");
#endif
    }
#endif

#if LOG_LEVEL >= 3
    Serial.println("[WiFi] Initialized");
#endif
}

bool WiFiManager::connect(const char* ssid, const char* password, uint32_t timeoutMs) {
    if (isConnected()) {
        return true;
    }

#if LOG_LEVEL >= 3
    Serial.print("[WiFi] Connecting to ");
    Serial.println(ssid);
#endif

    WiFi.begin(ssid, password);

    uint32_t startTime = millis();
    while (WiFi.status() != WL_CONNECTED) {
        if (millis() - startTime > timeoutMs) {
#if LOG_LEVEL >= 1
            Serial.println("[WiFi] Connection timeout!");
#endif
            WiFi.disconnect();
            return false;
        }
        delay(500);
#if LOG_LEVEL >= 4
        Serial.print(".");
#endif
    }

    _connected = true;

#if LOG_LEVEL >= 3
    Serial.println();
    Serial.print("[WiFi] Connected, IP: ");
    Serial.println(WiFi.localIP());
    Serial.print("[WiFi] RSSI: ");
    Serial.print(getRSSI());
    Serial.println(" dBm");
#endif

    return true;
}

void WiFiManager::disconnect() {
    WiFi.disconnect(true);
    _connected = false;

#if LOG_LEVEL >= 3
    Serial.println("[WiFi] Disconnected");
#endif
}

bool WiFiManager::isConnected() {
    _connected = (WiFi.status() == WL_CONNECTED);
    return _connected;
}

String WiFiManager::getLocalIP() {
    if (!isConnected()) {
        return String();
    }
    return WiFi.localIP().toString();
}

int8_t WiFiManager::getRSSI() {
    if (!isConnected()) {
        return 0;
    }
    return WiFi.RSSI();
}

void WiFiManager::setStaticIP(const char* ip, const char* gateway, const char* subnet) {
    IPAddress localIP, gatewayIP, subnetMask;
    localIP.fromString(ip);
    gatewayIP.fromString(gateway);
    subnetMask.fromString(subnet);

    WiFi.config(localIP, gatewayIP, subnetMask);

#if LOG_LEVEL >= 3
    Serial.print("[WiFi] Static IP configured: ");
    Serial.println(ip);
#endif
}

// 解析 MAC 地址字符串 (AA:BB:CC:DD:EE:FF 或 AA-BB-CC-DD-EE-FF)
static bool parseMacAddress(const char* macStr, uint8_t* macArray) {
    if (macStr == nullptr || strlen(macStr) < 17) {
        return false;
    }

    char temp[18];
    strncpy(temp, macStr, 17);
    temp[17] = '\0';

    // 统一替换 '-' 为 ':'
    for (int i = 0; temp[i]; i++) {
        if (temp[i] == '-') temp[i] = ':';
    }

    int values[6];
    if (sscanf(temp, "%02x:%02x:%02x:%02x:%02x:%02x",
               &values[0], &values[1], &values[2],
               &values[3], &values[4], &values[5]) == 6) {
        for (int i = 0; i < 6; i++) {
            macArray[i] = (uint8_t)values[i];
        }
        return true;
    }
    return false;
}
