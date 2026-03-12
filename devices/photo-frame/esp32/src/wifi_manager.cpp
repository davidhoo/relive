#include "wifi_manager.h"
#include "log.h"
#include <esp_wifi.h>
#include <esp_mac.h>

// 将字符串 MAC 地址解析为字节数组
static bool parseMACString(const char* macStr, uint8_t* macBytes) {
    if (macStr == nullptr || strlen(macStr) < 17) {
        return false;
    }

    unsigned int values[6];
    char separator = macStr[2];

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

WiFiManager::WiFiManager()
    : _connected(false), _usingCustomMAC(false), _officeMode(false),
      _lastReconnectAttempt(0), _scannedCount(0) {}

bool WiFiManager::parseCustomMAC() {
#ifdef USE_CUSTOM_MAC_ADDRESS
#ifdef CUSTOM_MAC_ADDRESS_STRING
    if (!parseMACString(CUSTOM_MAC_ADDRESS_STRING, _customMAC)) {
        DEBUG_SERIAL.println("[WiFi] 自定义 MAC 地址格式无效，使用默认 MAC");
        return false;
    }
#elif defined(CUSTOM_MAC_ADDRESS)
    uint8_t macArray[] = CUSTOM_MAC_ADDRESS;
    memcpy(_customMAC, macArray, 6);
#else
    DEBUG_SERIAL.println("[WiFi] 未定义自定义 MAC 地址，使用默认 MAC");
    return false;
#endif

    bool isNonZero = false;
    for (int i = 0; i < 6; i++) {
        if (_customMAC[i] != 0x00) { isNonZero = true; break; }
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

bool WiFiManager::applyBaseMAC() {
    DEBUG_SERIAL.printf("[WiFi] 设置系统 base MAC: %02X:%02X:%02X:%02X:%02X:%02X\n",
                       _customMAC[0], _customMAC[1], _customMAC[2],
                       _customMAC[3], _customMAC[4], _customMAC[5]);

    esp_err_t result = esp_base_mac_addr_set(_customMAC);
    if (result != ESP_OK) {
        DEBUG_SERIAL.printf("[WiFi] 设置 base MAC 失败，错误码: %d\n", result);
        return false;
    }
    DEBUG_SERIAL.println("[WiFi] 系统 base MAC 设置成功");
    return true;
}

void WiFiManager::verifyMAC() {
    uint8_t actualMAC[6] = {0};
    esp_wifi_get_mac(WIFI_IF_STA, actualMAC);

    DEBUG_SERIAL.printf("[WiFi] 实际 MAC: %02X:%02X:%02X:%02X:%02X:%02X\n",
                       actualMAC[0], actualMAC[1], actualMAC[2],
                       actualMAC[3], actualMAC[4], actualMAC[5]);

    if (_usingCustomMAC) {
        bool match = (memcmp(actualMAC, _customMAC, 6) == 0);
        DEBUG_SERIAL.printf("[WiFi] MAC 验证: %s\n", match ? "一致" : "不一致");
    }
}

bool WiFiManager::isUsingCustomMAC() {
    return _usingCustomMAC;
}

bool WiFiManager::isOfficeMode() {
    return _officeMode;
}

bool WiFiManager::scanForOfficeSSID() {
    LOG_INFO("[WiFi] 开始 WiFi 扫描...");
    _scannedCount = 0;
    _officeMode = false;

    int n = WiFi.scanNetworks(false, false, false, 300);
    LOG_INFO_F("[WiFi] 扫描到 %d 个网络\n", n);

    bool officeFound = false;
    String officeSSID = OFFICE_SSID;

    for (int i = 0; i < n && _scannedCount < 20; i++) {
        String ssid = WiFi.SSID(i);
        if (ssid.length() == 0) continue;

        // 去重
        bool dup = false;
        for (int j = 0; j < _scannedCount; j++) {
            if (_scannedSSIDs[j] == ssid) { dup = true; break; }
        }
        if (!dup) {
            _scannedSSIDs[_scannedCount++] = ssid;
        }

        if (officeSSID.length() > 0 && ssid == officeSSID) {
            officeFound = true;
        }
    }

    WiFi.scanDelete();

    if (officeFound) {
        LOG_INFO("[WiFi] 发现办公室 SSID，进入办公室模式");
        _officeMode = true;
    }
    return officeFound;
}

String* WiFiManager::getScannedSSIDs(int& count) {
    count = _scannedCount;
    return _scannedSSIDs;
}

bool WiFiManager::waitForConnection(int timeoutMs) {
    int elapsed = 0;
    while (WiFi.status() != WL_CONNECTED && elapsed < timeoutMs) {
        delay(500);
        DEBUG_SERIAL.print(".");
        elapsed += 500;
    }
    DEBUG_SERIAL.println();
    return WiFi.status() == WL_CONNECTED;
}

bool WiFiManager::begin() {
    DEBUG_SERIAL.println("[WiFi] 初始化...");

    // 如果定义了自定义 MAC，必须在第一次 WiFi.mode() 之前设置
    // esp_base_mac_addr_set 必须在 esp_wifi_init 之前调用
    _usingCustomMAC = parseCustomMAC();
    if (_usingCustomMAC) {
        applyBaseMAC();
    }

    // 扫描网络
    WiFi.mode(WIFI_STA);
    delay(100);
    scanForOfficeSSID();

    if (_officeMode) {
        // 办公室模式：使用编译时凭据连接（自定义 MAC 已在上面设置）
        WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
        LOG_INFO_F("[WiFi] 办公室模式，连接到: %s\n", WIFI_SSID);

        if (waitForConnection(30000)) {
            _connected = true;
            LOG_INFO("[WiFi] 连接成功!");
            LOG_INFO_F("[WiFi] IP: %s\n", WiFi.localIP().toString().c_str());
            verifyMAC();
            LOG_INFO_F("[WiFi] RSSI: %d dBm\n", WiFi.RSSI());
            return true;
        }

        _connected = false;
        LOG_ERROR("[WiFi] 办公室模式连接失败");
        return false;
    }

    // 非办公室模式：返回 false，由 main 决定使用 NVS 凭据或 AP 配网
    LOG_INFO("[WiFi] 非办公室模式");
    return false;
}

bool WiFiManager::connectWithCredentials(const String& ssid, const String& pass) {
    LOG_INFO_F("[WiFi] 连接到: %s\n", ssid.c_str());

    WiFi.disconnect(true);
    delay(100);
    WiFi.mode(WIFI_STA);
    delay(100);
    WiFi.begin(ssid.c_str(), pass.c_str());

    if (waitForConnection(30000)) {
        _connected = true;
        LOG_INFO("[WiFi] 连接成功!");
        LOG_INFO_F("[WiFi] IP: %s\n", WiFi.localIP().toString().c_str());
        LOG_INFO_F("[WiFi] RSSI: %d dBm\n", WiFi.RSSI());
        return true;
    }

    _connected = false;
    LOG_ERROR("[WiFi] 连接失败");
    return false;
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
    LOG_DEBUG("[WiFi] 已断开连接");
}

bool WiFiManager::reconnect() {
    unsigned long currentMillis = millis();
    if (currentMillis - _lastReconnectAttempt < RECONNECT_INTERVAL) {
        return false;
    }
    _lastReconnectAttempt = currentMillis;

    LOG_INFO("[WiFi] 尝试重新连接...");
    WiFi.disconnect();
    delay(1000);

    WiFi.begin(WIFI_SSID, WIFI_PASSWORD);

    if (waitForConnection(15000)) {
        _connected = true;
        LOG_INFO("[WiFi] 重连成功!");
        LOG_INFO_F("[WiFi] IP: %s\n", WiFi.localIP().toString().c_str());
        verifyMAC();
        return true;
    }

    LOG_ERROR("[WiFi] 重连失败");
    return false;
}

bool WiFiManager::startAP() {
    LOG_INFO_F("[WiFi] 启动 AP 热点: %s\n", AP_SSID);

    WiFi.disconnect(true);
    delay(100);
    WiFi.mode(WIFI_AP);
    delay(100);

    bool result = WiFi.softAP(AP_SSID);
    if (result) {
        LOG_INFO_F("[WiFi] AP 已启动, IP: %s\n", WiFi.softAPIP().toString().c_str());
    } else {
        LOG_ERROR("[WiFi] AP 启动失败");
    }
    return result;
}

void WiFiManager::stopAP() {
    WiFi.softAPdisconnect(true);
    WiFi.mode(WIFI_STA);
    LOG_INFO("[WiFi] AP 已停止");
}
