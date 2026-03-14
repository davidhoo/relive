#ifndef NVS_CONFIG_H
#define NVS_CONFIG_H

#include <Arduino.h>

class NVSConfig {
public:
    void begin();

    // 配置标志
    bool isConfigured();
    void setConfigured(bool val);

    // WiFi
    String getWiFiSSID();
    String getWiFiPass();
    void setWiFi(const String& ssid, const String& pass);

    // Server
    String getServerHost();
    uint16_t getServerPort();
    String getAPIKey();
    void setServer(const String& host, uint16_t port, const String& apiKey);

    // Schedules
    String getSchedules();
    void setSchedules(const String& schedules);

    // AP fail count (退避用)
    uint8_t getAPFailCount();
    void setAPFailCount(uint8_t count);
    void resetAPFailCount();

    // 双击上电检测（快速断电重启进入 AP 配网）
    bool getBootFlag();
    void setBootFlag(bool val);
};

#endif // NVS_CONFIG_H
