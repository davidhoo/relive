# Relive ESP32 墨水屏相框

基于 ESP32-S3 和 7.3 寸 E Ink Spectra 6 彩色墨水屏的智能相框。通过定时深度睡眠唤醒，从 Relive 服务器获取"往年今日"照片并显示。

## 硬件规格

- **主控**: ESP32-S3 (4MB Flash, 8MB PSRAM OPI)
- **屏幕**: Good Display GDEP073E01 (7.3 寸 E Ink Spectra 6)
  - 分辨率: 800x480
  - 颜色: 6 色 (黑、白、黄、红、蓝、绿)
  - 接口: SPI
- **连接**: WiFi 2.4GHz

## 功能特性

- 从服务器获取预渲染的 4-bit 二进制图像并显示
- 6 色墨水屏显示
- 定时睡眠调度（HHMM 格式，如 `"0800,2000"`）
- 深度睡眠低功耗（睡眠电流 ~10μA）
- 双配置源：Office 模式（编译时配置）与 NVS 模式（AP 配网）
- AP 配网门户（SSID: `relive`, Web 配置页面）
- AP 超时退避睡眠（30min → 60min → 180min）
- NTP + 服务器时间校准（`X-Server-Time` 响应头）
- `config_local.h` 编译时覆盖配置

## API 接口

设备仅使用一个接口获取预渲染的二进制图像：

```
GET /api/v1/device/display.bin
Header: X-API-Key: {device_api_key}
Response: 二进制图像数据 (4-bit/像素, 192,000 字节)
Response Headers: X-Checksum, X-Asset-ID, X-Server-Time
```

## 硬件连接

| 屏幕引脚 | ESP32-S3 GPIO |
|---------|--------------|
| BUSY    | GPIO 10      |
| RST     | GPIO 11      |
| DC      | GPIO 12      |
| CS      | GPIO 13      |
| MOSI    | GPIO 14      |
| SCK     | GPIO 15      |

## 配置说明

### 基本配置

在 `include/config.h` 中定义了默认值，所有 `#define` 均使用 `#ifndef` 守卫，可通过 `config_local.h` 覆盖：

```cpp
#define WIFI_SSID "your_wifi_ssid"        // WiFi 名称
#define WIFI_PASSWORD "your_wifi_password"    // WiFi 密码
// 服务器地址支持以下格式：
// - 纯 IP: "192.168.1.100"
// - 带协议: "https://your-server.example.com"
#define SERVER_HOST "192.168.1.100"
#define SERVER_PORT 8080
#define DEVICE_API_KEY "your_api_key"
#define DEFAULT_SCHEDULES "0800,2000"      // 刷新时间点 (HHMM 格式，逗号分隔)
```

### config_local.h 覆盖

创建 `include/config_local.h` 覆盖默认配置（该文件应加入 `.gitignore`）：

```cpp
#ifndef CONFIG_LOCAL_H
#define CONFIG_LOCAL_H

#define WIFI_SSID "my_wifi"
#define WIFI_PASSWORD "my_password"
#define SERVER_HOST "https://my-server.example.com"
#define SERVER_PORT 8888
#define DEVICE_API_KEY "sk-relive-xxxxxxxx"
#define DEFAULT_SCHEDULES "0800,1200,1800"

#endif
```

### 自定义 MAC 地址（可选）

```cpp
#define USE_CUSTOM_MAC_ADDRESS
#define CUSTOM_MAC_ADDRESS_STRING "AA:BB:CC:DD:EE:FF"
```

## 工作模式

### 双配置源

设备启动时扫描 WiFi，根据环境自动选择模式：

1. **Office 模式**：扫描到 `OFFICE_SSID` 时启用，使用编译时配置（WiFi 凭证、调度时间）
2. **NVS 模式**：使用 AP 配网门户写入 NVS 的配置

### AP 配网门户

首次使用或 WiFi 连接失败时自动进入 AP 模式：

- SSID: `relive`（无密码）
- 配置地址: `http://192.168.4.1`（端口 80）
- 提供 Web 页面配置 WiFi、服务器地址、API Key、刷新时间
- 配置提交后自动连接并重启

AP 超时策略：
- 3 分钟无客户端连接则超时
- 有客户端连接时重置超时计时
- 超时后尝试已有 NVS 配置
- 全部失败进入退避深度睡眠：第 1 次 30 分钟，第 2 次 60 分钟，第 3 次及之后 180 分钟

### 定时睡眠调度

- 调度格式：逗号分隔的 `HHMM` 或 `HH:MM`，如 `"0800,2000"`
- 完成照片显示后计算到下一个时间点的间隔，进入深度睡眠
- 若距下个时间点不足 60 秒，跳到再下一个
- 时间来源：优先使用服务器 `X-Server-Time` 响应头，其次 NTP (`pool.ntp.org`, GMT+8)

## 数据格式

屏幕使用 **4-bit/像素** 格式，每字节包含 2 个像素（高 4 位 + 低 4 位）：

| 值 (4-bit) | 颜色  |
|-----------|------|
| 0x0       | 黑色  |
| 0x1       | 白色  |
| 0x2       | 黄色  |
| 0x3       | 红色  |
| 0x5       | 蓝色  |
| 0x6       | 绿色  |

缓冲区大小：800 x 480 / 2 = **192,000 字节**（实际分配 200,000 字节含余量，优先使用 PSRAM）

## 编译上传

使用 [PlatformIO](https://platformio.org/) + [pioarduino](https://github.com/pioarduino/platform-espressif32) (ESP32 Arduino Core 3.x)：

```bash
# 编译
pio run

# 编译并上传
pio run --target upload

# 或使用 VS Code + PlatformIO 扩展，点击 Upload 按钮
```

### 依赖库

- `ArduinoJson` ^7
- `Adafruit GFX Library` ^1.11
- `Adafruit BusIO` ^1.16

## 调试

串口参数: 115200 baud

日志级别可在 `config.h` 或 `config_local.h` 中设置：

```cpp
#define LOG_LEVEL 3  // 0=OFF, 1=ERROR, 2=INFO, 3=DEBUG
```

启用 `DEBUG_MODE` 后使用固定间隔刷新（`REFRESH_INTERVAL_MS`），不走调度逻辑。

## 注意事项

1. 首次使用前需在 Relive 管理后台创建 `embedded` 类型设备并获取 API Key
2. 确保 ESP32-S3 和服务器网络可达
3. 屏幕刷新需要约 10-20 秒，期间不要断电
4. 建议使用稳定的电源供应 (5V/2A)
5. `config_local.h` 包含敏感信息，不应提交到版本库
