#ifndef WIFI_MANAGER_H
#define WIFI_MANAGER_H

#include <Arduino.h>
#include <WiFi.h>
#include "config.h"

class WiFiManager {
public:
    WiFiManager();

    // 初始化并连接 WiFi（先扫描，判断模式）
    // 返回 true=已连接, false=需要 AP 配网
    bool begin();

    // 用指定凭据连接
    bool connectWithCredentials(const String& ssid, const String& pass);

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

    // WiFi 扫描是否发现 OFFICE_SSID
    bool scanForOfficeSSID();

    // 当前是否办公室模式
    bool isOfficeMode();

    // 启动 AP 热点
    bool startAP();

    // 停止 AP 热点
    void stopAP();

    // 返回扫描到的 SSID 列表
    String* getScannedSSIDs(int& count);

private:
    bool _connected;
    bool _usingCustomMAC;
    bool _officeMode;
    uint8_t _customMAC[6];
    unsigned long _lastReconnectAttempt;
    static const unsigned long RECONNECT_INTERVAL = 30000;

    // 扫描结果
    String _scannedSSIDs[20];
    int _scannedCount;

    // 解析自定义 MAC 配置
    bool parseCustomMAC();

    // 设置系统级 base MAC（WiFi 初始化之前调用）
    bool applyBaseMAC();

    // 验证实际 MAC 地址
    void verifyMAC();

    // 内部连接（带等待）
    bool waitForConnection(int timeoutMs);
};

#endif // WIFI_MANAGER_H
