# ESP32 通信协议设计

> ESP32 电子相框与后端服务的通信协议定义
> 最后更新：2026-02-28
> 版本：v1.0

---

## 目录

- [一、概述](#一概述)
- [二、网络架构](#二网络架构)
- [三、API 接口](#三api-接口)
- [四、数据格式](#四数据格式)
- [五、图片传输](#五图片传输)
- [六、设备管理](#六设备管理)
- [七、错误处理](#七错误处理)
- [八、安全认证](#八安全认证)
- [九、低功耗策略](#九低功耗策略)
- [十、OTA 升级](#十ota-升级)

---

## 一、概述

### 1.1 通信方式

**协议**：HTTP/HTTPS over WiFi 2.4GHz

**架构**：
```
┌──────────────┐         WiFi          ┌──────────────┐
│              │  ◄─────────────────►  │              │
│  ESP32-S3    │    HTTP/HTTPS         │  Relive      │
│  墨水屏相框   │                       │  Backend     │
│              │                       │  (NAS)       │
└──────────────┘                       └──────────────┘
```

**特点**：
- ✅ 简单可靠（基于 HTTP）
- ✅ 易于调试
- ✅ 支持 HTTPS 加密（可选）
- ✅ 低功耗（深度睡眠 + 定时唤醒）

### 1.2 工作流程

```
1. 开机/定时唤醒
   ↓
2. 连接 WiFi
   ↓
3. 发送心跳（上报状态）
   ↓
4. 获取展示照片信息
   ↓
5. 下载照片图片
   ↓
6. 显示到墨水屏
   ↓
7. 进入深度睡眠（等待下次唤醒）
```

### 1.3 通信频率

| 场景 | 频率 | 说明 |
|------|------|------|
| **正常模式** | 每天 1-2 次 | 早上 8:00、晚上 20:00 |
| **手动刷新** | 按需 | 按下按钮立即刷新 |
| **配置模式** | 持续连接 | WiFi 配置、OTA 升级时 |

---

## 二、网络架构

### 2.1 网络拓扑

```
┌──────────────────────────────────────────┐
│  家庭局域网（192.168.1.0/24）             │
│                                          │
│  ┌────────────┐      ┌────────────┐     │
│  │  WiFi 路由  │      │  群晖 NAS   │     │
│  │  ZINFOID_02Q │─────►│  192.168.1.100 │     │
│  └─────┬──────┘      └────────────┘     │
│        │                   ↑              │
│        │                   │ Docker       │
│        │             ┌─────┴────┐         │
│        │             │  Relive  │         │
│        │             │  :8080   │         │
│        │             └──────────┘         │
│        │                                  │
│        │ WiFi 2.4GHz                      │
│        ↓                                  │
│  ┌────────────┐                          │
│  │  ESP32-S3  │                          │
│  │  墨水屏相框 │                          │
│  └────────────┘                          │
│                                          │
└──────────────────────────────────────────┘
```

### 2.2 IP 地址配置

**后端服务**：
- 内网地址：`http://192.168.1.100:8080`（或 `http://192.168.1.100:8080`）
- 域名（可选）：`http://relive.local:8080`（使用 mDNS）

**ESP32 设备**：
- DHCP 自动获取 IP
- 保存后端服务地址到配置

### 2.3 DNS 解析

**方案 1：硬编码 IP**
```cpp
const char* SERVER_HOST = "192.168.1.100";
const int SERVER_PORT = 8080;
```

**方案 2：mDNS**（推荐）
```cpp
const char* SERVER_HOST = "relive.local";
const int SERVER_PORT = 8080;
```

---

## 三、API 接口

### 3.1 接口清单

ESP32 需要调用以下 API：

| 接口 | 方法 | 路径 | 说明 |
|------|------|------|------|
| **设备注册** | POST | `/api/v1/esp32/register` | 首次注册设备 |
| **设备心跳** | POST | `/api/v1/esp32/heartbeat` | 上报状态 |
| **获取展示照片** | GET | `/api/v1/esp32/display/photo` | 获取要展示的照片信息 |
| **下载照片图片** | GET | `/api/v1/esp32/image/{photo_id}` | 下载墨水屏图片 |
| **上报展示记录** | POST | `/api/v1/esp32/display/record` | 记录展示历史 |
| **获取配置** | GET | `/api/v1/esp32/config` | 获取设备配置 |
| **检查更新** | GET | `/api/v1/esp32/ota/check` | 检查固件更新 |

### 3.2 设备注册

**请求**：
```http
POST /api/v1/esp32/register HTTP/1.1
Host: 192.168.1.100:8080
Content-Type: application/json

{
  "device_id": "ESP32-ABCD1234",
  "name": "客厅相框",
  "screen_width": 800,
  "screen_height": 480,
  "firmware_version": "1.0.0",
  "ip_address": "192.168.1.150",
  "mac_address": "AA:BB:CC:DD:EE:FF"
}
```

**响应**：
```json
{
  "success": true,
  "data": {
    "device_id": "ESP32-ABCD1234",
    "api_key": "sk-esp32-xxxxxxxxxxxxxxxx",
    "config": {
      "refresh_hour": [8, 20],
      "brightness": 100
    }
  },
  "message": "设备注册成功"
}
```

### 3.3 设备心跳

**请求**：
```http
POST /api/v1/esp32/heartbeat HTTP/1.1
Host: 192.168.1.100:8080
Authorization: Bearer sk-esp32-xxxxxxxxxxxxxxxx
Content-Type: application/json

{
  "device_id": "ESP32-ABCD1234",
  "battery_level": 85,
  "wifi_rssi": -45,
  "free_heap": 120000,
  "last_display_photo_id": 12345,
  "firmware_version": "1.0.0"
}
```

**响应**：
```json
{
  "success": true,
  "data": {
    "server_time": "2026-02-28T10:30:00Z",
    "next_refresh_in_seconds": 43200,
    "has_new_firmware": false
  },
  "message": "心跳成功"
}
```

### 3.4 获取展示照片

**请求**：
```http
GET /api/v1/esp32/display/photo?device_id=ESP32-ABCD1234 HTTP/1.1
Host: 192.168.1.100:8080
Authorization: Bearer sk-esp32-xxxxxxxxxxxxxxxx
```

**响应**：
```json
{
  "success": true,
  "data": {
    "photo_id": 12345,
    "file_path": "/volume1/photos/2023/03/IMG_5678.jpg",
    "taken_at": "2023-03-15T14:30:00Z",
    "caption": "春日阳光下的笑容",
    "description": "在公园里享受温暖的午后时光",
    "memory_score": 95,
    "beauty_score": 88,
    "location": "杭州·西湖",
    "image_url": "/api/v1/esp32/image/12345",
    "image_width": 800,
    "image_height": 480,
    "image_size": 45678
  },
  "message": "获取成功"
}
```

### 3.5 下载照片图片

**请求**：
```http
GET /api/v1/esp32/image/12345 HTTP/1.1
Host: 192.168.1.100:8080
Authorization: Bearer sk-esp32-xxxxxxxxxxxxxxxx
```

**响应**：
- Content-Type: `image/jpeg`
- 图片数据（二进制）

**特殊处理**：
- 后端自动调整图片到墨水屏尺寸（800×480）
- 自动应用抖动算法（7色或16色）
- 图片大小 < 100KB

### 3.6 上报展示记录

**请求**：
```http
POST /api/v1/esp32/display/record HTTP/1.1
Host: 192.168.1.100:8080
Authorization: Bearer sk-esp32-xxxxxxxxxxxxxxxx
Content-Type: application/json

{
  "device_id": "ESP32-ABCD1234",
  "photo_id": 12345,
  "displayed_at": "2026-02-28T10:30:00Z",
  "display_duration": 86400,
  "trigger_type": "scheduled"
}
```

**响应**：
```json
{
  "success": true,
  "message": "记录成功"
}
```

---

## 四、数据格式

### 4.1 通用响应格式

所有 API 返回统一格式：

```json
{
  "success": true,          // 是否成功
  "data": { ... },          // 数据（成功时）
  "error": {                // 错误信息（失败时）
    "code": "ERROR_CODE",
    "message": "错误描述"
  },
  "message": "操作结果描述"
}
```

### 4.2 时间格式

统一使用 **ISO 8601** 格式（UTC）：

```json
{
  "taken_at": "2026-02-28T10:30:00Z",
  "displayed_at": "2026-02-28T10:30:00+08:00"
}
```

**ESP32 处理**：
- 接收 UTC 时间
- 转换为本地时间（配置时区）

### 4.3 触发类型

```cpp
enum TriggerType {
  TRIGGER_SCHEDULED = "scheduled",   // 定时刷新
  TRIGGER_MANUAL = "manual",         // 手动刷新（按钮）
  TRIGGER_BOOT = "boot"              // 开机刷新
};
```

---

## 五、图片传输

### 5.1 图片处理流程

```
后端处理：
1. 读取原始照片
   ↓
2. 调整到墨水屏尺寸（800×480）
   ↓
3. 应用抖动算法（7色或16色）
   ↓
4. JPEG 压缩（质量 85%）
   ↓
5. 返回给 ESP32

ESP32 处理：
1. 下载图片（HTTP）
   ↓
2. 解码 JPEG
   ↓
3. 转换为墨水屏格式
   ↓
4. 显示到屏幕
```

### 5.2 图片尺寸

**墨水屏规格**（GDEP073E01）：
- 分辨率：800×480 像素
- 颜色：7色（黑、白、红、黄、蓝、绿、橙）

**后端返回图片**：
- 尺寸：800×480（精确匹配）
- 格式：JPEG
- 大小：< 100KB
- 质量：85%

### 5.3 图片缓存

**ESP32 缓存策略**：
```cpp
// 保存最后一张照片到 SPIFFS
void cachePhoto(uint8_t* imageData, size_t size) {
  File file = SPIFFS.open("/cache/last_photo.jpg", "w");
  file.write(imageData, size);
  file.close();
}

// 断网时显示缓存照片
void displayCachedPhoto() {
  if (SPIFFS.exists("/cache/last_photo.jpg")) {
    File file = SPIFFS.open("/cache/last_photo.jpg", "r");
    // 显示缓存照片
  }
}
```

---

## 六、设备管理

### 6.1 设备注册流程

```
1. ESP32 首次启动
   ↓
2. 进入 WiFi 配置模式（AP 模式）
   ↓
3. 用户手机连接 ESP32 热点
   ↓
4. 配置 WiFi SSID 和密码
   ↓
5. 配置后端服务地址
   ↓
6. ESP32 连接 WiFi
   ↓
7. 调用注册接口
   ↓
8. 获取 API Key
   ↓
9. 保存配置到 NVS
```

### 6.2 设备标识

**device_id 生成**：
```cpp
String generateDeviceID() {
  uint8_t mac[6];
  WiFi.macAddress(mac);
  char deviceID[32];
  sprintf(deviceID, "ESP32-%02X%02X%02X%02X",
          mac[2], mac[3], mac[4], mac[5]);
  return String(deviceID);
}
```

示例：`ESP32-ABCD1234`

### 6.3 设备配置

**配置参数**：
```json
{
  "refresh_hour": [8, 20],        // 刷新时间（小时）
  "brightness": 100,              // 亮度（0-100）
  "timezone": "Asia/Shanghai",    // 时区
  "sleep_mode": "deep",           // 睡眠模式（deep/light）
  "ota_enabled": true             // 是否启用 OTA
}
```

**保存到 NVS**：
```cpp
#include <Preferences.h>

Preferences prefs;

void saveConfig(const char* key, const char* value) {
  prefs.begin("relive", false);
  prefs.putString(key, value);
  prefs.end();
}

String loadConfig(const char* key) {
  prefs.begin("relive", true);
  String value = prefs.getString(key, "");
  prefs.end();
  return value;
}
```

---

## 七、错误处理

### 7.1 HTTP 状态码

| 状态码 | 说明 | ESP32 处理 |
|--------|------|-----------|
| 200 OK | 成功 | 正常处理 |
| 400 Bad Request | 请求参数错误 | 记录日志，使用默认值 |
| 401 Unauthorized | API Key 无效 | 重新注册 |
| 404 Not Found | 照片不存在 | 显示缓存照片 |
| 429 Too Many Requests | 请求过于频繁 | 延迟重试 |
| 500 Internal Server Error | 服务器错误 | 稍后重试 |
| 503 Service Unavailable | 服务不可用 | 显示缓存照片 |

### 7.2 错误代码

```json
{
  "success": false,
  "error": {
    "code": "PHOTO_NOT_FOUND",
    "message": "未找到可展示的照片"
  }
}
```

**错误代码清单**：
- `INVALID_API_KEY` - API Key 无效
- `DEVICE_NOT_FOUND` - 设备未注册
- `PHOTO_NOT_FOUND` - 照片不存在
- `IMAGE_GENERATION_FAILED` - 图片生成失败
- `RATE_LIMIT_EXCEEDED` - 请求过于频繁

### 7.3 重试策略

```cpp
const int MAX_RETRIES = 3;
const int RETRY_DELAY_MS = 5000;

bool requestWithRetry(const char* url) {
  for (int i = 0; i < MAX_RETRIES; i++) {
    HTTPClient http;
    http.begin(url);
    int httpCode = http.GET();

    if (httpCode == 200) {
      // 成功
      http.end();
      return true;
    }

    // 失败，等待后重试
    http.end();
    if (i < MAX_RETRIES - 1) {
      delay(RETRY_DELAY_MS);
    }
  }

  // 重试失败
  return false;
}
```

### 7.4 降级策略

**网络故障时**：
1. 显示缓存的最后一张照片
2. 显示错误提示（可选）
3. 进入睡眠，等待下次重试

**后端故障时**：
1. 使用缓存照片
2. 延长睡眠时间（避免频繁重试）
3. 记录错误日志

---

## 八、安全认证

### 8.1 API Key 认证

**生成规则**：
```
api_key = "sk-esp32-" + random(32字符)
```

示例：`sk-esp32-a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6`

**使用方式**：
```http
GET /api/v1/esp32/display/photo HTTP/1.1
Authorization: Bearer sk-esp32-xxxxxxxxxxxxxxxx
```

### 8.2 HTTPS 支持（可选）

**证书验证**：
```cpp
#include <WiFiClientSecure.h>

WiFiClientSecure client;

void setupHTTPS() {
  // 选项 1：跳过证书验证（不安全，仅测试）
  client.setInsecure();

  // 选项 2：使用根证书（推荐）
  // client.setCACert(root_ca);
}
```

**推荐方案**：
- 内网环境：使用 HTTP（简单）
- 公网环境：使用 HTTPS（安全）

### 8.3 数据加密

**敏感数据存储**：
```cpp
// API Key 加密存储到 NVS
void saveAPIKey(const char* apiKey) {
  // 简单加密（XOR）
  String encrypted = xorEncrypt(apiKey, "secret");
  prefs.putString("api_key", encrypted);
}

String loadAPIKey() {
  String encrypted = prefs.getString("api_key", "");
  return xorDecrypt(encrypted, "secret");
}
```

---

## 九、低功耗策略

### 9.1 深度睡眠

**睡眠流程**：
```cpp
void enterDeepSleep(uint64_t sleepSeconds) {
  Serial.printf("进入深度睡眠 %llu 秒\n", sleepSeconds);

  // 配置唤醒源（定时器）
  esp_sleep_enable_timer_wakeup(sleepSeconds * 1000000ULL);

  // 配置唤醒源（按钮）
  esp_sleep_enable_ext0_wakeup(GPIO_NUM_0, LOW);

  // 进入深度睡眠
  esp_deep_sleep_start();
}
```

**睡眠时长计算**：
```cpp
uint64_t calculateSleepTime() {
  // 获取当前时间
  struct tm timeinfo;
  if (!getLocalTime(&timeinfo)) {
    return 12 * 3600; // 默认 12 小时
  }

  // 计算到下次刷新的时间
  int currentHour = timeinfo.tm_hour;
  int nextRefreshHour;

  if (currentHour < 8) {
    nextRefreshHour = 8;
  } else if (currentHour < 20) {
    nextRefreshHour = 20;
  } else {
    nextRefreshHour = 8 + 24; // 明天早上 8 点
  }

  int sleepHours = nextRefreshHour - currentHour;
  return sleepHours * 3600;
}
```

### 9.2 WiFi 省电

```cpp
void optimizeWiFi() {
  // 降低发射功率（内网信号强度足够）
  WiFi.setTxPower(WIFI_POWER_11dBm);

  // 启用 WiFi 省电模式
  WiFi.setSleep(true);
}
```

### 9.3 功耗估算

**硬件配置**：
- ESP32-S3：正常 ~80mA，深度睡眠 ~10μA
- 墨水屏：刷新时 ~200mA，静态 0mA
- 电池：2×18650（7.4V, 3000mAh）

**功耗计算**（每天刷新 2 次）：
```
工作时间：5 分钟/次 × 2 次 = 10 分钟/天
工作功耗：(80 + 200) mA × 10/60 小时 = 46.7 mAh/天

睡眠时间：23小时50分/天
睡眠功耗：0.01 mA × 23.83 小时 = 0.24 mAh/天

总功耗：46.7 + 0.24 ≈ 47 mAh/天

电池续航：6000 mAh / 47 mAh ≈ 127 天 ≈ 4个月
```

---

## 十、OTA 升级

### 10.1 检查更新

**请求**：
```http
GET /api/v1/esp32/ota/check?device_id=ESP32-ABCD1234&version=1.0.0 HTTP/1.1
Host: 192.168.1.100:8080
Authorization: Bearer sk-esp32-xxxxxxxxxxxxxxxx
```

**响应**：
```json
{
  "success": true,
  "data": {
    "has_update": true,
    "latest_version": "1.1.0",
    "firmware_url": "/api/v1/esp32/ota/download/1.1.0",
    "firmware_size": 1024000,
    "release_notes": "修复了 WiFi 连接问题",
    "mandatory": false
  }
}
```

### 10.2 下载固件

**请求**：
```http
GET /api/v1/esp32/ota/download/1.1.0 HTTP/1.1
Host: 192.168.1.100:8080
Authorization: Bearer sk-esp32-xxxxxxxxxxxxxxxx
```

**响应**：
- Content-Type: `application/octet-stream`
- 固件二进制数据

### 10.3 OTA 实现

```cpp
#include <Update.h>

bool performOTA(const char* firmwareURL) {
  HTTPClient http;
  http.begin(firmwareURL);

  int httpCode = http.GET();
  if (httpCode != 200) {
    http.end();
    return false;
  }

  int contentLength = http.getSize();
  bool canBegin = Update.begin(contentLength);

  if (!canBegin) {
    http.end();
    return false;
  }

  // 下载并写入固件
  WiFiClient* stream = http.getStreamPtr();
  size_t written = Update.writeStream(*stream);

  if (written == contentLength) {
    Serial.println("OTA 下载完成");
  }

  if (Update.end()) {
    if (Update.isFinished()) {
      Serial.println("OTA 成功，重启中...");
      ESP.restart();
      return true;
    }
  }

  http.end();
  return false;
}
```

---

## 十一、示例代码

### 11.1 完整工作流程

```cpp
#include <WiFi.h>
#include <HTTPClient.h>
#include <ArduinoJson.h>

// 配置
const char* WIFI_SSID = "your-wifi-ssid";
const char* WIFI_PASSWORD = "your-wifi-password";
const char* SERVER_HOST = "192.168.1.100";
const int SERVER_PORT = 8080;
String apiKey = "";
String deviceID = "";

void setup() {
  Serial.begin(115200);

  // 1. 连接 WiFi
  connectWiFi();

  // 2. 加载配置
  loadConfiguration();

  // 3. 如果未注册，先注册
  if (apiKey.isEmpty()) {
    registerDevice();
  }

  // 4. 发送心跳
  sendHeartbeat();

  // 5. 获取并显示照片
  displayPhoto();

  // 6. 进入深度睡眠
  uint64_t sleepTime = calculateSleepTime();
  enterDeepSleep(sleepTime);
}

void loop() {
  // 不会执行到这里（深度睡眠后会重启）
}

void connectWiFi() {
  WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
  while (WiFi.status() != WL_CONNECTED) {
    delay(500);
    Serial.print(".");
  }
  Serial.println("\nWiFi 连接成功");
}

void registerDevice() {
  HTTPClient http;
  String url = String("http://") + SERVER_HOST + ":" + SERVER_PORT + "/api/v1/esp32/register";

  http.begin(url);
  http.addHeader("Content-Type", "application/json");

  // 构造请求
  StaticJsonDocument<512> doc;
  doc["device_id"] = deviceID;
  doc["name"] = "客厅相框";
  doc["screen_width"] = 800;
  doc["screen_height"] = 480;
  doc["firmware_version"] = "1.0.0";

  String requestBody;
  serializeJson(doc, requestBody);

  int httpCode = http.POST(requestBody);
  if (httpCode == 200) {
    String response = http.getString();

    // 解析响应
    StaticJsonDocument<1024> responseDoc;
    deserializeJson(responseDoc, response);

    apiKey = responseDoc["data"]["api_key"].as<String>();
    saveConfiguration();

    Serial.println("设备注册成功");
  }

  http.end();
}

void displayPhoto() {
  // 1. 获取照片信息
  HTTPClient http;
  String url = String("http://") + SERVER_HOST + ":" + SERVER_PORT +
               "/api/v1/esp32/display/photo?device_id=" + deviceID;

  http.begin(url);
  http.addHeader("Authorization", "Bearer " + apiKey);

  int httpCode = http.GET();
  if (httpCode == 200) {
    String response = http.getString();

    StaticJsonDocument<2048> doc;
    deserializeJson(doc, response);

    int photoID = doc["data"]["photo_id"];
    String imageURL = doc["data"]["image_url"];
    String caption = doc["data"]["caption"];

    // 2. 下载并显示图片
    downloadAndDisplay(photoID, imageURL, caption);

    // 3. 上报展示记录
    reportDisplayRecord(photoID);
  }

  http.end();
}

void downloadAndDisplay(int photoID, String imageURL, String caption) {
  HTTPClient http;
  String url = String("http://") + SERVER_HOST + ":" + SERVER_PORT + imageURL;

  http.begin(url);
  http.addHeader("Authorization", "Bearer " + apiKey);

  int httpCode = http.GET();
  if (httpCode == 200) {
    int len = http.getSize();
    uint8_t* buffer = (uint8_t*)malloc(len);

    WiFiClient* stream = http.getStreamPtr();
    stream->readBytes(buffer, len);

    // 显示到墨水屏（省略具体实现）
    displayToEPaper(buffer, len, caption);

    // 缓存照片
    cachePhoto(buffer, len);

    free(buffer);
  }

  http.end();
}
```

---

## 十二、总结

### 12.1 协议特点

✅ **简单**：基于 HTTP，易于实现和调试
✅ **可靠**：重试机制、降级策略
✅ **安全**：API Key 认证、支持 HTTPS
✅ **省电**：深度睡眠 + 定时唤醒
✅ **灵活**：支持手动刷新、OTA 升级

### 12.2 实现优先级

**Phase 1：基础通信**
- ✅ 设备注册
- ✅ 心跳上报
- ✅ 获取展示照片
- ✅ 下载图片
- ✅ 上报展示记录

**Phase 2：优化**
- ✅ 图片缓存
- ✅ 错误处理
- ✅ 深度睡眠

**Phase 3：高级功能**
- ✅ OTA 升级
- ✅ HTTPS 支持
- ✅ WiFi 配置界面

---

**ESP32 通信协议设计完成** ✅
**准备硬件开发** 🚀
