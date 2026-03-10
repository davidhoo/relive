#ifndef WIFI_MANAGER_H
#define WIFI_MANAGER_H

#include <Arduino.h>
#include <WiFi.h>
#include "config.h"

class WiFiManager {
public:
    WiFiManager();

    // 初始化并连接 WiFi
    bool begin();

    // 检查连接状态
    bool isConnected();

    // 获取本地 IP 地址
    String getLocalIP();

    // 获取 MAC 地址（实际使用的 MAC）
    String getMACAddress();

    // 断开连接
    void disconnect();

    // 重新连接
    bool reconnect();

    // 是否使用了自定义 MAC 地址
    bool isUsingCustomMAC();

private:
    bool _connected;
    bool _usingCustomMAC;
    unsigned long _lastReconnectAttempt;
    static const unsigned long RECONNECT_INTERVAL = 30000; // 30秒重连间隔

    // 设置自定义 MAC 地址
    bool setupCustomMAC();
};

#endif // WIFI_MANAGER_H
