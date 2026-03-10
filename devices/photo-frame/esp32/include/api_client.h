/**
 * @file api_client.h
 * @brief Relive API 客户端（简化版）
 *
 * 只请求一个接口获取照片 bin 文件
 */

#ifndef API_CLIENT_H
#define API_CLIENT_H

#include <Arduino.h>
#include <HTTPClient.h>

// Bin 文件头部信息（从 HTTP Headers 获取）
struct BinHeaderInfo {
    uint32_t assetId;
    uint32_t photoId;
    char checksum[65];      // SHA256 hex string
    char renderProfile[32];
    char batchDate[11];     // YYYY-MM-DD
    uint16_t sequence;
    size_t contentLength;
    bool valid;
};

// Bin 文件数据结构
struct BinFileData {
    uint8_t* data;
    size_t size;
    BinHeaderInfo header;
    uint16_t width;
    uint16_t height;
    uint8_t paletteColors;
};

class ApiClient {
public:
    ApiClient();
    ~ApiClient();

    /**
     * @brief 设置 API 基础 URL
     */
    void setBaseUrl(const char* baseUrl);

    /**
     * @brief 设置 API Key
     */
    void setApiKey(const char* apiKey);

    /**
     * @brief 获取显示 bin 文件
     * @param outData 输出数据（内部 malloc，使用后需调用 freeBinData）
     * @return 是否成功
     */
    bool getDisplayBin(BinFileData& outData);

    /**
     * @brief 释放 bin 数据内存
     */
    void freeBinData(BinFileData& data);

    /**
     * @brief 获取最后一次错误信息
     */
    const char* getLastError();

    /**
     * @brief 获取 HTTP 状态码
     */
    int getLastHttpCode();

private:
    String _baseUrl;
    String _apiKey;
    String _lastError;
    int _lastHttpCode;

    bool parseBinFile(BinFileData& data);
    bool validateChecksum(BinFileData& data);
};

#endif // API_CLIENT_H
