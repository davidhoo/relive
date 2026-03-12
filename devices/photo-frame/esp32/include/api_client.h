#ifndef API_CLIENT_H
#define API_CLIENT_H

#include <Arduino.h>
#include <HTTPClient.h>
#include <WiFiClient.h>
#include <WiFiClientSecure.h>
#include "config.h"

class APIClient {
public:
    APIClient();

    // 初始化（使用编译时配置）
    void begin();

    // 使用动态配置初始化（NVS 模式）
    void beginWithConfig(const String& host, uint16_t port, const String& apiKey);

    // 下载 bin 文件到缓冲区
    // 返回：下载的字节数，-1 表示失败
    int downloadBinFile(uint8_t* buffer, size_t bufferSize, String& outChecksum);

    // 获取最后一次下载响应中的 X-Server-Time（Unix timestamp，0=未收到）
    long getLastServerTime();

    // 获取最后一次错误信息
    String getLastError();

    // 获取 HTTP 响应码
    int getLastHttpCode();

private:
    String _lastError;
    int _lastHttpCode;
    bool _useHTTPS;
    String _baseUrl;
    uint16_t _port;
    String _apiKey;
    long _lastServerTime;
    WiFiClient _wifiClient;

    // 解析 host 字符串，提取协议和 baseUrl
    void parseHost(const String& host);

    // 构建完整的 API URL
    String buildUrl(const char* endpoint);

    // 设置 HTTP 请求头
    void setHeaders(HTTPClient& http);

    // 解析响应头中的 X-Server-Time
    void parseServerTime(HTTPClient& http);

    // HTTP 下载 bin 文件
    int downloadBinFileHTTP(uint8_t* buffer, size_t bufferSize, String& outChecksum);

    // HTTPS 下载 bin 文件
    int downloadBinFileHTTPS(uint8_t* buffer, size_t bufferSize, String& outChecksum);
};

#endif // API_CLIENT_H
