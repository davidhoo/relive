#ifndef API_CLIENT_H
#define API_CLIENT_H

#include <Arduino.h>
#include <HTTPClient.h>
#include <WiFiClient.h>
#include <WiFiClientSecure.h>
#include "config.h"

// 显示信息结构体
struct DisplayInfo {
    String batchDate;
    int sequence;
    int totalCount;
    uint32_t photoID;
    uint32_t itemID;
    uint32_t assetID;
    String renderProfile;
    String binURL;
    String checksum;
    bool valid;
};

class APIClient {
public:
    APIClient();

    // 初始化
    void begin();

    // 获取显示信息（JSON 元数据）
    DisplayInfo getDisplayInfo();

    // 下载 bin 文件到缓冲区
    // 返回：下载的字节数，-1 表示失败
    int downloadBinFile(uint8_t* buffer, size_t bufferSize, String& outChecksum);

    // HTTP 下载 bin 文件
    int downloadBinFileHTTP(uint8_t* buffer, size_t bufferSize, String& outChecksum);

    // HTTPS 下载 bin 文件
    int downloadBinFileHTTPS(uint8_t* buffer, size_t bufferSize, String& outChecksum);

    // 获取最后一次错误信息
    String getLastError();

    // 获取 HTTP 响应码
    int getLastHttpCode();

private:
    String _lastError;
    int _lastHttpCode;
    bool _useHTTPS;
    String _baseUrl;
    WiFiClient _wifiClient;  // 持久化的WiFi客户端

    // 解析服务器配置，初始化客户端
    void setupServer();

    // 构建完整的 API URL
    String buildUrl(const char* endpoint);

    // 设置 HTTP 请求头
    void setHeaders(HTTPClient& http);

    // 解析显示信息 JSON
    DisplayInfo parseDisplayInfo(const String& json);

    // HTTP 获取显示信息
    DisplayInfo getDisplayInfoHTTP();

    // HTTPS 获取显示信息
    DisplayInfo getDisplayInfoHTTPS();
};

#endif // API_CLIENT_H
