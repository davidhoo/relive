#include "api_client.h"
#include <ArduinoJson.h>

#if LOG_LEVEL >= 2
#define LOG_INFO(msg) DEBUG_SERIAL.println(msg)
#define LOG_INFO_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_INFO(msg)
#define LOG_INFO_F(msg, ...)
#endif

#if LOG_LEVEL >= 3
#define LOG_DEBUG(msg) DEBUG_SERIAL.println(msg)
#define LOG_DEBUG_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_DEBUG(msg)
#define LOG_DEBUG_F(msg, ...)
#endif

#if LOG_LEVEL >= 1
#define LOG_ERROR(msg) DEBUG_SERIAL.println(msg)
#define LOG_ERROR_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_ERROR(msg)
#define LOG_ERROR_F(msg, ...)
#endif

APIClient::APIClient() : _lastHttpCode(0), _useHTTPS(false) {}

void APIClient::begin() {
    setupServer();
}

void APIClient::setupServer() {
    String host = SERVER_HOST;
    host.trim();

    // 检查是否已包含协议前缀
    if (host.startsWith("http://")) {
        _useHTTPS = false;
        _baseUrl = host.substring(7); // 去掉 "http://"
    } else if (host.startsWith("https://")) {
        _useHTTPS = true;
        _baseUrl = host.substring(8); // 去掉 "https://"
    } else {
        // 没有协议前缀，默认使用 HTTP
        _useHTTPS = false;
        _baseUrl = host;
    }

    // 去除末尾的斜杠
    if (_baseUrl.endsWith("/")) {
        _baseUrl = _baseUrl.substring(0, _baseUrl.length() - 1);
    }

    LOG_INFO_F("[API] 服务器: %s, 协议: %s\n", _baseUrl.c_str(), _useHTTPS ? "HTTPS" : "HTTP");
}

String APIClient::buildUrl(const char* endpoint) {
    String url = _useHTTPS ? "https://" : "http://";
    url += _baseUrl;
    url += ":";
    url += String(SERVER_PORT);
    url += endpoint;
    return url;
}

void APIClient::setHeaders(HTTPClient& http) {
    http.addHeader("X-API-Key", DEVICE_API_KEY);
    http.addHeader("Accept", "application/octet-stream, application/json");
}

DisplayInfo APIClient::getDisplayInfo() {
    if (_useHTTPS) {
        return getDisplayInfoHTTPS();
    } else {
        return getDisplayInfoHTTP();
    }
}

DisplayInfo APIClient::getDisplayInfoHTTP() {
    DisplayInfo info;
    info.valid = false;

    HTTPClient http;
    String url = "http://" + _baseUrl + ":" + String(SERVER_PORT) + "/api/v1/device/display";

    LOG_INFO_F("[API] HTTP 请求: %s\n", url.c_str());

    WiFiClient client;
    client.setTimeout(HTTP_TIMEOUT_MS / 1000);

    http.begin(client, url);
    setHeaders(http);
    http.addHeader("Accept", "application/json");

    _lastHttpCode = http.GET();

    if (_lastHttpCode == HTTP_CODE_OK) {
        String payload = http.getString();
        LOG_DEBUG_F("[API] 响应: %s\n", payload.c_str());
        info = parseDisplayInfo(payload);
    } else {
        _lastError = "HTTP " + String(_lastHttpCode);
        LOG_ERROR_F("[API] HTTP 请求失败: %d\n", _lastHttpCode);
    }

    http.end();
    return info;
}

DisplayInfo APIClient::getDisplayInfoHTTPS() {
    DisplayInfo info;
    info.valid = false;

    HTTPClient http;
    String url = "https://" + _baseUrl + ":" + String(SERVER_PORT) + "/api/v1/device/display";

    LOG_INFO_F("[API] HTTPS 请求: %s\n", url.c_str());

    WiFiClientSecure client;
    client.setInsecure();
    client.setTimeout(HTTP_TIMEOUT_MS / 1000);
    client.setHandshakeTimeout(30);

    http.begin(client, url);
    setHeaders(http);
    http.addHeader("Accept", "application/json");

    _lastHttpCode = http.GET();

    if (_lastHttpCode == HTTP_CODE_OK) {
        String payload = http.getString();
        LOG_DEBUG_F("[API] 响应: %s\n", payload.c_str());
        info = parseDisplayInfo(payload);
    } else {
        LOG_ERROR_F("[API] HTTPS 请求失败: %d\n", _lastHttpCode);
    }

    http.end();
    return info;
}

DisplayInfo APIClient::parseDisplayInfo(const String& json) {
    DisplayInfo info;
    info.valid = false;

    StaticJsonDocument<1024> doc;
    DeserializationError error = deserializeJson(doc, json);

    if (error) {
        _lastError = "JSON解析失败: " + String(error.c_str());
        LOG_ERROR_F("[API] JSON解析错误: %s\n", error.c_str());
        return info;
    }

    if (!doc["success"]) {
        _lastError = "API返回失败";
        const char* msg = doc["error"]["message"];
        if (msg) {
            _lastError += ": ";
            _lastError += msg;
        }
        LOG_ERROR_F("[API] 错误: %s\n", _lastError.c_str());
        return info;
    }

    JsonObject data = doc["data"];
    if (data.isNull()) {
        _lastError = "无数据";
        return info;
    }

    info.batchDate = data["batch_date"] | "";
    info.sequence = data["sequence"] | 0;
    info.totalCount = data["total_count"] | 0;
    info.photoID = data["photo_id"] | 0;
    info.itemID = data["item_id"] | 0;
    info.assetID = data["asset_id"] | 0;
    info.renderProfile = data["render_profile"] | "";
    info.binURL = data["bin_url"] | "";
    info.checksum = data["checksum"] | "";
    info.valid = true;

    LOG_INFO_F("[API] 照片 ID: %d, Asset ID: %d\n", info.photoID, info.assetID);
    LOG_INFO_F("[API] Render Profile: %s\n", info.renderProfile.c_str());

    return info;
}

int APIClient::downloadBinFile(uint8_t* buffer, size_t bufferSize, String& outChecksum) {
    if (_useHTTPS) {
        return downloadBinFileHTTPS(buffer, bufferSize, outChecksum);
    } else {
        return downloadBinFileHTTP(buffer, bufferSize, outChecksum);
    }
}

int APIClient::downloadBinFileHTTP(uint8_t* buffer, size_t bufferSize, String& outChecksum) {
    HTTPClient http;
    String url = "http://" + _baseUrl + ":" + String(SERVER_PORT) + "/api/v1/device/display.bin";

    LOG_INFO_F("[API] HTTP 下载: %s\n", url.c_str());

    WiFiClient client;
    client.setTimeout(HTTP_TIMEOUT_MS / 1000);

    http.begin(client, url);
    setHeaders(http);

    _lastHttpCode = http.GET();

    LOG_INFO_F("[API] HTTP 响应码: %d\n", _lastHttpCode);

    if (_lastHttpCode != HTTP_CODE_OK) {
        _lastError = "HTTP " + String(_lastHttpCode);
        LOG_ERROR_F("[API] HTTP 下载失败: %d\n", _lastHttpCode);
        http.end();
        return -1;
    }

    // 获取响应头信息 - 尝试不同的大小写格式
    outChecksum = http.header("x-checksum");
    if (outChecksum.length() == 0) outChecksum = http.header("X-Checksum");

    String contentLength = http.header("content-length");
    if (contentLength.length() == 0) contentLength = http.header("Content-Length");

    String assetID = http.header("x-asset-id");
    if (assetID.length() == 0) assetID = http.header("X-Asset-Id");
    if (assetID.length() == 0) assetID = http.header("X-Asset-ID");

    // 调试输出所有响应头
    LOG_INFO_F("[API] 响应头: X-Checksum=%s\n", outChecksum.c_str());
    LOG_INFO_F("[API] 响应头: Content-Length=%s\n", contentLength.c_str());
    LOG_INFO_F("[API] 响应头: X-Asset-ID=%s\n", assetID.c_str());

    // 如果没有 Content-Length，尝试从流传输获取
    if (contentLength.length() == 0) {
        int streamLen = http.getSize();
        if (streamLen > 0) {
            contentLength = String(streamLen);
            LOG_INFO_F("[API] 从流获取大小: %d\n", streamLen);
        }
    }

    LOG_INFO_F("[API] Asset ID: %s\n", assetID.c_str());
    LOG_INFO_F("[API] Checksum: %s\n", outChecksum.c_str());
    LOG_INFO_F("[API] Content-Length: %s\n", contentLength.c_str());

    int totalLength = contentLength.toInt();
    if (totalLength <= 0) {
        _lastError = "无效的内容长度";
        http.end();
        return -1;
    }

    if ((size_t)totalLength > bufferSize) {
        _lastError = "缓冲区太小";
        LOG_ERROR_F("[API] 缓冲区不足: 需要 %d, 只有 %d\n", totalLength, bufferSize);
        http.end();
        return -1;
    }

    // 读取数据
    WiFiClient* stream = http.getStreamPtr();
    int downloaded = 0;
    unsigned long timeout = millis() + HTTP_TIMEOUT_MS;

    while (downloaded < totalLength && millis() < timeout) {
        int available = stream->available();
        if (available > 0) {
            int toRead = min(available, totalLength - downloaded);
            int bytesRead = stream->readBytes(buffer + downloaded, toRead);
            downloaded += bytesRead;

            if (downloaded % 1024 == 0 || downloaded == totalLength) {
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

int APIClient::downloadBinFileHTTPS(uint8_t* buffer, size_t bufferSize, String& outChecksum) {
    HTTPClient http;
    String url = "https://" + _baseUrl + ":" + String(SERVER_PORT) + "/api/v1/device/display.bin";

    LOG_INFO_F("[API] HTTPS 下载: %s\n", url.c_str());

    WiFiClientSecure client;
    client.setInsecure();
    client.setTimeout(HTTP_TIMEOUT_MS / 1000);
    client.setHandshakeTimeout(30);

    http.begin(client, url);
    setHeaders(http);

    _lastHttpCode = http.GET();

    if (_lastHttpCode != HTTP_CODE_OK) {
        _lastError = "HTTP " + String(_lastHttpCode);
        LOG_ERROR_F("[API] 下载失败: %d\n", _lastHttpCode);
        http.end();
        return -1;
    }

    // 获取响应头信息 - 尝试不同的大小写格式
    outChecksum = http.header("x-checksum");
    if (outChecksum.length() == 0) outChecksum = http.header("X-Checksum");

    String contentLength = http.header("content-length");
    if (contentLength.length() == 0) contentLength = http.header("Content-Length");

    String assetID = http.header("x-asset-id");
    if (assetID.length() == 0) assetID = http.header("X-Asset-Id");
    if (assetID.length() == 0) assetID = http.header("X-Asset-ID");

    LOG_INFO_F("[API] Asset ID: %s\n", assetID.c_str());
    LOG_INFO_F("[API] Checksum: %s\n", outChecksum.c_str());
    LOG_INFO_F("[API] Content-Length: %s\n", contentLength.c_str());

    // 如果没有 Content-Length，尝试从流传输获取
    if (contentLength.length() == 0 || contentLength.toInt() <= 0) {
        int streamLen = http.getSize();
        if (streamLen > 0) {
            contentLength = String(streamLen);
            LOG_INFO_F("[API] 从流获取大小: %d\n", streamLen);
        }
    }

    int totalLength = contentLength.toInt();
    if (totalLength <= 0) {
        _lastError = "无效的内容长度";
        http.end();
        return -1;
    }

    if ((size_t)totalLength > bufferSize) {
        _lastError = "缓冲区太小";
        LOG_ERROR_F("[API] 缓冲区不足: 需要 %d, 只有 %d\n", totalLength, bufferSize);
        http.end();
        return -1;
    }

    // 读取数据
    WiFiClient* stream = http.getStreamPtr();
    int downloaded = 0;
    unsigned long timeout = millis() + HTTP_TIMEOUT_MS;

    while (downloaded < totalLength && millis() < timeout) {
        int available = stream->available();
        if (available > 0) {
            int toRead = min(available, totalLength - downloaded);
            int bytesRead = stream->readBytes(buffer + downloaded, toRead);
            downloaded += bytesRead;

            if (downloaded % 1024 == 0 || downloaded == totalLength) {
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
