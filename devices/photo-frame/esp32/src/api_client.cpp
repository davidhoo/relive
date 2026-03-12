#include "api_client.h"
#include "log.h"
#include <ArduinoJson.h>

APIClient::APIClient() : _lastHttpCode(0), _useHTTPS(false), _port(SERVER_PORT), _lastServerTime(0) {}

void APIClient::parseHost(const String& host) {
    String h = host;
    h.trim();

    if (h.startsWith("http://")) {
        _useHTTPS = false;
        _baseUrl = h.substring(7);
    } else if (h.startsWith("https://")) {
        _useHTTPS = true;
        _baseUrl = h.substring(8);
    } else {
        _useHTTPS = false;
        _baseUrl = h;
    }

    if (_baseUrl.endsWith("/")) {
        _baseUrl = _baseUrl.substring(0, _baseUrl.length() - 1);
    }

    LOG_INFO_F("[API] 服务器: %s, 端口: %d, 协议: %s\n",
               _baseUrl.c_str(), _port, _useHTTPS ? "HTTPS" : "HTTP");
}

void APIClient::begin() {
    _port = SERVER_PORT;
    _apiKey = DEVICE_API_KEY;
    parseHost(SERVER_HOST);
}

void APIClient::beginWithConfig(const String& host, uint16_t port, const String& apiKey) {
    _port = port;
    _apiKey = apiKey;
    parseHost(host);
}

String APIClient::buildUrl(const char* endpoint) {
    String url = _useHTTPS ? "https://" : "http://";
    url += _baseUrl;
    url += ":";
    url += String(_port);
    url += endpoint;
    return url;
}

void APIClient::setHeaders(HTTPClient& http) {
    http.addHeader("X-API-Key", _apiKey);
    http.addHeader("Accept", "application/octet-stream, application/json");
}

void APIClient::parseServerTime(HTTPClient& http) {
    String timeStr = http.header("X-Server-Time");
    if (timeStr.length() == 0) timeStr = http.header("x-server-time");
    if (timeStr.length() > 0) {
        _lastServerTime = timeStr.toInt();
        LOG_INFO_F("[API] X-Server-Time: %ld\n", _lastServerTime);
    } else {
        _lastServerTime = 0;
    }
}

long APIClient::getLastServerTime() {
    return _lastServerTime;
}

int APIClient::downloadBinFile(uint8_t* buffer, size_t bufferSize, String& outChecksum) {
    _lastServerTime = 0;
    if (_useHTTPS) {
        return downloadBinFileHTTPS(buffer, bufferSize, outChecksum);
    } else {
        return downloadBinFileHTTP(buffer, bufferSize, outChecksum);
    }
}

int APIClient::downloadBinFileHTTP(uint8_t* buffer, size_t bufferSize, String& outChecksum) {
    String url = buildUrl("/api/v1/device/display.bin");

    LOG_INFO_F("[API] HTTP 下载: %s\n", url.c_str());

    _wifiClient.stop();
    delay(50);
    _wifiClient.setTimeout(HTTP_TIMEOUT_MS / 1000);

    HTTPClient http;

    if (!http.begin(_wifiClient, url)) {
        _lastError = "HTTP 连接初始化失败";
        LOG_ERROR("[API] HTTP begin() 失败\n");
        return -1;
    }

    setHeaders(http);

    const char* headerKeys[] = {
        "X-Checksum", "x-checksum", "Content-Length", "content-length",
        "X-Asset-ID", "x-asset-id", "X-Server-Time", "x-server-time"
    };
    http.collectHeaders(headerKeys, sizeof(headerKeys) / sizeof(headerKeys[0]));

    LOG_INFO("[API] 发送 GET 请求...\n");
    _lastHttpCode = http.GET();
    LOG_INFO_F("[API] HTTP 响应码: %d\n", _lastHttpCode);

    if (_lastHttpCode != HTTP_CODE_OK) {
        _lastError = "HTTP " + String(_lastHttpCode);
        LOG_ERROR_F("[API] HTTP 下载失败: %d\n", _lastHttpCode);
        http.end();
        _wifiClient.stop();
        return -1;
    }

    // 解析 X-Server-Time
    parseServerTime(http);

    outChecksum = http.header("X-Checksum");
    if (outChecksum.length() == 0) outChecksum = http.header("x-checksum");

    String assetID = http.header("X-Asset-ID");
    if (assetID.length() == 0) assetID = http.header("x-asset-id");

    LOG_INFO_F("[API] 响应头: X-Checksum=%s\n", outChecksum.c_str());
    LOG_INFO_F("[API] 响应头: X-Asset-ID=%s\n", assetID.c_str());

    int totalLength = http.getSize();
    LOG_INFO_F("[API] Content-Length: %d\n", totalLength);

    if (totalLength <= 0) {
        _lastError = "无效的内容长度";
        LOG_ERROR("[API] 无法获取内容长度\n");
        http.end();
        _wifiClient.stop();
        return -1;
    }

    if ((size_t)totalLength > bufferSize) {
        _lastError = "缓冲区太小";
        LOG_ERROR_F("[API] 缓冲区不足: 需要 %d, 只有 %d\n", totalLength, bufferSize);
        http.end();
        _wifiClient.stop();
        return -1;
    }

    LOG_INFO("[API] 开始读取数据...\n");

    int downloaded = 0;
    uint8_t* writePtr = buffer;
    int remaining = totalLength;

    WiFiClient* stream = http.getStreamPtr();
    if (!stream) {
        _lastError = "无法获取数据流";
        LOG_ERROR("[API] getStreamPtr() 返回 NULL\n");
        http.end();
        _wifiClient.stop();
        return -1;
    }

    const int CHUNK_SIZE = 512;
    unsigned long lastProgress = millis();
    unsigned long timeout = millis() + HTTP_TIMEOUT_MS;

    while (remaining > 0 && millis() < timeout) {
        int retries = 0;
        while (!stream->available() && retries < 100) {
            delay(10);
            retries++;
            if (millis() >= timeout) break;
        }

        if (!stream->available()) {
            LOG_ERROR("[API] 数据流超时\n");
            break;
        }

        int toRead = min(stream->available(), remaining);
        toRead = min(toRead, CHUNK_SIZE);

        int bytesRead = stream->readBytes(writePtr, toRead);

        if (bytesRead > 0) {
            downloaded += bytesRead;
            writePtr += bytesRead;
            remaining -= bytesRead;

            if (millis() - lastProgress >= 1000 || remaining == 0) {
                LOG_INFO_F("[API] 进度: %d / %d bytes (%.1f%%)\n",
                          downloaded, totalLength, (downloaded * 100.0) / totalLength);
                lastProgress = millis();
            }
        } else if (bytesRead == 0) {
            delay(10);
        } else {
            LOG_ERROR_F("[API] 读取错误: %d\n", bytesRead);
            break;
        }
    }

    http.end();
    delay(50);
    _wifiClient.stop();
    delay(50);

    if (downloaded != totalLength) {
        _lastError = "下载不完整";
        LOG_ERROR_F("[API] 下载不完整: %d / %d\n", downloaded, totalLength);
        return -1;
    }

    LOG_INFO_F("[API] 下载完成: %d bytes\n", downloaded);
    return downloaded;
}

int APIClient::downloadBinFileHTTPS(uint8_t* buffer, size_t bufferSize, String& outChecksum) {
    HTTPClient http;
    String url = buildUrl("/api/v1/device/display.bin");

    LOG_INFO_F("[API] HTTPS 下载: %s\n", url.c_str());

    WiFiClientSecure client;
    client.setInsecure();
    client.setTimeout(HTTP_TIMEOUT_MS / 1000);
    client.setHandshakeTimeout(30);

    if (!http.begin(client, url)) {
        _lastError = "HTTPS 连接初始化失败";
        LOG_ERROR("[API] HTTPS begin() 失败\n");
        return -1;
    }

    setHeaders(http);

    const char* headerKeys[] = {
        "X-Checksum", "x-checksum", "Content-Length", "content-length",
        "X-Asset-ID", "x-asset-id", "X-Server-Time", "x-server-time"
    };
    http.collectHeaders(headerKeys, sizeof(headerKeys) / sizeof(headerKeys[0]));

    LOG_DEBUG("[API] 开始 HTTPS GET 请求...\n");
    _lastHttpCode = http.GET();
    LOG_INFO_F("[API] HTTPS 响应码: %d\n", _lastHttpCode);

    if (_lastHttpCode < 0) {
        _lastError = "HTTPS 连接错误: " + String(_lastHttpCode);
        LOG_ERROR_F("[API] HTTPS 连接错误: %d (可能是 TLS 握手失败)\n", _lastHttpCode);
        http.end();
        return -1;
    }

    if (_lastHttpCode != HTTP_CODE_OK) {
        _lastError = "HTTPS " + String(_lastHttpCode);
        LOG_ERROR_F("[API] HTTPS 下载失败: %d\n", _lastHttpCode);
        http.end();
        return -1;
    }

    // 解析 X-Server-Time
    parseServerTime(http);

    outChecksum = http.header("X-Checksum");
    if (outChecksum.length() == 0) outChecksum = http.header("x-checksum");

    String assetID = http.header("X-Asset-ID");
    if (assetID.length() == 0) assetID = http.header("x-asset-id");

    LOG_INFO_F("[API] 响应头: X-Checksum=%s\n", outChecksum.c_str());
    LOG_INFO_F("[API] 响应头: X-Asset-ID=%s\n", assetID.c_str());

    int totalLength = http.getSize();
    LOG_INFO_F("[API] Content-Length: %d\n", totalLength);

    if (totalLength <= 0) {
        _lastError = "无效的内容长度";
        LOG_ERROR("[API] 无法获取内容长度\n");
        http.end();
        return -1;
    }

    if ((size_t)totalLength > bufferSize) {
        _lastError = "缓冲区太小";
        LOG_ERROR_F("[API] 缓冲区不足: 需要 %d, 只有 %d\n", totalLength, bufferSize);
        http.end();
        return -1;
    }

    WiFiClient* stream = http.getStreamPtr();
    int downloaded = 0;
    unsigned long timeout = millis() + HTTP_TIMEOUT_MS;

    while (downloaded < totalLength && millis() < timeout) {
        int available = stream->available();
        if (available > 0) {
            int toRead = min(available, totalLength - downloaded);
            int bytesRead = stream->readBytes(buffer + downloaded, toRead);
            downloaded += bytesRead;

            if (downloaded % 4096 == 0 || downloaded == totalLength) {
                LOG_DEBUG_F("[API] 已下载: %d / %d bytes\n", downloaded, totalLength);
            }
        }
        delay(1);
    }

    http.end();

    if (downloaded != totalLength) {
        _lastError = "下载不完整";
        LOG_ERROR_F("[API] 下载不完整: %d / %d\n", downloaded, totalLength);
        return -1;
    }

    LOG_INFO_F("[API] 下载完成: %d bytes\n", downloaded);
    return downloaded;
}

String APIClient::getLastError() {
    return _lastError;
}

int APIClient::getLastHttpCode() {
    return _lastHttpCode;
}
