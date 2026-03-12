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
};

#endif // NVS_CONFIG_H
