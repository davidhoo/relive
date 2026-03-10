/**
 * @file wifi_manager.h
 * @brief WiFi 连接管理器
 */

#ifndef WIFI_MANAGER_H
#define WIFI_MANAGER_H

#include <Arduino.h>
#include <WiFi.h>

class WiFiManager {
public:
    WiFiManager();

    /**
     * @brief 初始化 WiFi
     */
    void begin();

    /**
     * @brief 连接到 WiFi 网络
     * @param ssid 网络名称
     * @param password 密码
     * @param timeoutMs 超时时间（毫秒）
     * @return 是否连接成功
     */
    bool connect(const char* ssid, const char* password, uint32_t timeoutMs = 30000);

    /**
     * @brief 断开连接
     */
    void disconnect();

    /**
     * @brief 检查是否已连接
     */
    bool isConnected();

    /**
     * @brief 获取本地 IP 地址
     */
    String getLocalIP();

    /**
     * @brief 获取信号强度 (RSSI)
     */
    int8_t getRSSI();

    /**
     * @brief 设置静态 IP
     */
    void setStaticIP(const char* ip, const char* gateway, const char* subnet);

private:
    bool _connected;
    uint32_t _lastReconnectAttempt;
    static const uint32_t RECONNECT_INTERVAL = 60000; // 1分钟
};

#endif // WIFI_MANAGER_H
