/**
 * @file api_client.cpp
 * @brief Relive API 客户端实现
 */

#include "api_client.h"
#include "config.h"
#include <mbedtls/sha256.h>

ApiClient::ApiClient() : _lastHttpCode(0) {
}

ApiClient::~ApiClient() {
}

void ApiClient::setBaseUrl(const char* baseUrl) {
    _baseUrl = baseUrl;
}

void ApiClient::setApiKey(const char* apiKey) {
    _apiKey = apiKey;
}

const char* ApiClient::getLastError() {
    return _lastError.c_str();
}

int ApiClient::getLastHttpCode() {
    return _lastHttpCode;
}

bool ApiClient::getDisplayBin(BinFileData& outData) {
    outData.data = nullptr;
    outData.size = 0;
    outData.header.valid = false;

    if (_baseUrl.isEmpty() || _apiKey.isEmpty()) {
        _lastError = "API URL or API Key not configured";
        return false;
    }

    HTTPClient http;
    String url = _baseUrl + API_ENDPOINT_DISPLAY_BIN;

#if LOG_LEVEL >= 3
    Serial.print("[API] Requesting: ");
    Serial.println(url);
#endif

    http.begin(url);
    http.addHeader(API_HEADER_API_KEY, _apiKey);
    http.setTimeout(HTTP_TIMEOUT_MS);

    _lastHttpCode = http.GET();

    if (_lastHttpCode != HTTP_CODE_OK) {
        _lastError = "HTTP error: " + String(_lastHttpCode);
#if LOG_LEVEL >= 1
        Serial.print("[API] Error: ");
        Serial.println(_lastError);
#endif
        http.end();
        return false;
    }

    // 获取响应头信息
    outData.header.contentLength = http.getSize();
    String assetId = http.header("X-Asset-ID");
    String photoId = http.header("X-Photo-ID");
    String checksum = http.header("X-Checksum");
    String renderProfile = http.header("X-Render-Profile");
    String batchDate = http.header("X-Batch-Date");
    String sequence = http.header("X-Sequence");

#if LOG_LEVEL >= 3
    Serial.print("[API] Content-Length: ");
    Serial.println(outData.header.contentLength);
    Serial.print("[API] Asset ID: ");
    Serial.println(assetId);
    Serial.print("[API] Checksum: ");
    Serial.println(checksum);
#endif

    // 解析头部信息
    outData.header.assetId = assetId.toInt();
    outData.header.photoId = photoId.toInt();
    strncpy(outData.header.checksum, checksum.c_str(), sizeof(outData.header.checksum) - 1);
    outData.header.checksum[sizeof(outData.header.checksum) - 1] = '\0';
    strncpy(outData.header.renderProfile, renderProfile.c_str(), sizeof(outData.header.renderProfile) - 1);
    outData.header.renderProfile[sizeof(outData.header.renderProfile) - 1] = '\0';
    strncpy(outData.header.batchDate, batchDate.c_str(), sizeof(outData.header.batchDate) - 1);
    outData.header.batchDate[sizeof(outData.header.batchDate) - 1] = '\0';
    outData.header.sequence = sequence.toInt();
    outData.header.valid = true;

    // 分配内存并下载数据
    if (outData.header.contentLength == 0 || outData.header.contentLength > BIN_FILE_MAX_SIZE) {
        _lastError = "Invalid content size: " + String(outData.header.contentLength);
#if LOG_LEVEL >= 1
        Serial.println("[API] " + _lastError);
#endif
        http.end();
        return false;
    }

    outData.data = (uint8_t*)malloc(outData.header.contentLength);
    if (!outData.data) {
        _lastError = "Failed to allocate memory";
#if LOG_LEVEL >= 1
        Serial.println("[API] " + _lastError);
#endif
        http.end();
        return false;
    }

    // 下载数据
    size_t downloaded = 0;
    WiFiClient* stream = http.getStreamPtr();
    uint32_t lastProgressTime = millis();

    while (http.connected() && downloaded < outData.header.contentLength) {
        size_t available = stream->available();
        if (available) {
            size_t toRead = min(available, (size_t)(outData.header.contentLength - downloaded));
            size_t bytesRead = stream->readBytes(outData.data + downloaded, toRead);
            downloaded += bytesRead;

            // 每 10KB 打印进度
            if (millis() - lastProgressTime > 1000) {
#if LOG_LEVEL >= 3
                Serial.print("[API] Downloaded: ");
                Serial.print(downloaded / 1024);
                Serial.println(" KB");
#endif
                lastProgressTime = millis();
            }
        } else {
            delay(10);
        }
    }

    http.end();

    if (downloaded != outData.header.contentLength) {
        _lastError = "Download incomplete: " + String(downloaded) + "/" + String(outData.header.contentLength);
#if LOG_LEVEL >= 1
        Serial.println("[API] " + _lastError);
#endif
        free(outData.data);
        outData.data = nullptr;
        return false;
    }

    outData.size = downloaded;

#if LOG_LEVEL >= 3
    Serial.print("[API] Download complete: ");
    Serial.print(downloaded);
    Serial.println(" bytes");
#endif

    // 解析 bin 文件
    if (!parseBinFile(outData)) {
        free(outData.data);
        outData.data = nullptr;
        return false;
    }

    // 验证校验和
    if (!validateChecksum(outData)) {
        free(outData.data);
        outData.data = nullptr;
        return false;
    }

    return true;
}

bool ApiClient::parseBinFile(BinFileData& data) {
    if (data.size < 16) {
        _lastError = "Bin file too small";
        return false;
    }

    // 检查魔数 "RLVD"
    if (data.data[0] != 'R' || data.data[1] != 'L' ||
        data.data[2] != 'V' || data.data[3] != 'D') {
        _lastError = "Invalid magic number";
        return false;
    }

    // 检查版本
    uint8_t version = data.data[4];
    if (version != 1) {
        _lastError = "Unsupported version: " + String(version);
        return false;
    }

    // 解析基本信息
    data.paletteColors = data.data[5];
    uint8_t ditherModeLen = data.data[6];

    // 解析宽高 (little-endian)
    data.width = data.data[8] | (data.data[9] << 8);
    data.height = data.data[10] | (data.data[11] << 8);

#if LOG_LEVEL >= 3
    Serial.print("[API] Bin format: ");
    Serial.print(data.width);
    Serial.print("x");
    Serial.print(data.height);
    Serial.print(", ");
    Serial.print(data.paletteColors);
    Serial.println(" colors");
#endif

    // 计算有效数据起始位置 (跳过 header 和 dither mode string)
    size_t headerSize = 12 + ditherModeLen;
    if (data.size < headerSize) {
        _lastError = "Invalid bin file structure";
        return false;
    }

    return true;
}

bool ApiClient::validateChecksum(BinFileData& data) {
    if (!data.header.valid || strlen(data.header.checksum) != 64) {
        _lastError = "Invalid checksum header";
        return false;
    }

    // 计算 SHA256
    uint8_t hash[32];
    mbedtls_sha256_context ctx;
    mbedtls_sha256_init(&ctx);
    mbedtls_sha256_starts(&ctx, 0);
    mbedtls_sha256_update(&ctx, data.data, data.size);
    mbedtls_sha256_finish(&ctx, hash);
    mbedtls_sha256_free(&ctx);

    // 转换为 hex string
    char hashHex[65];
    for (int i = 0; i < 32; i++) {
        sprintf(hashHex + (i * 2), "%02x", hash[i]);
    }
    hashHex[64] = '\0';

    // 比较
    if (strcasecmp(hashHex, data.header.checksum) != 0) {
        _lastError = "Checksum mismatch";
#if LOG_LEVEL >= 1
        Serial.println("[API] Checksum mismatch!");
        Serial.print("[API] Expected: ");
        Serial.println(data.header.checksum);
        Serial.print("[API] Actual:   ");
        Serial.println(hashHex);
#endif
        return false;
    }

#if LOG_LEVEL >= 3
    Serial.println("[API] Checksum verified");
#endif

    return true;
}

void ApiClient::freeBinData(BinFileData& data) {
    if (data.data) {
        free(data.data);
        data.data = nullptr;
    }
    data.size = 0;
    data.header.valid = false;
}
