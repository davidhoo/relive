# Relive ESP32 墨水屏相框

基于 ESP32-S3 和 7.3寸 E Ink Spectra 6 彩色墨水屏的智能相框。

## 硬件规格

- **主控**: ESP32-S3 (16MB Flash, 8MB PSRAM)
- **屏幕**: Good Display GDEP073E01 (7.3寸 E Ink Spectra 6)
  - 分辨率: 800x480
  - 颜色: 6色 (黑、白、红、黄、蓝、绿)
  - 接口: SPI
- **连接**: WiFi

## 功能特性

- 自动从服务器获取照片并显示
- 支持 6 种颜色显示 (黑、白、红、黄、蓝、绿)
- Deep Sleep 低功耗模式
- 定时刷新 (默认 5 分钟)

## API 接口

设备通过以下接口与服务器通信：

### 获取显示信息
```
GET /api/v1/device/display
Header: X-API-Key: {device_api_key}
```

### 获取显示图像 (bin 文件)
```
GET /api/v1/device/display.bin
Header: X-API-Key: {device_api_key}
Response: 二进制图像数据 (3bit/像素, 6色格式)
```

## 配置说明

在 `include/config.h` 中修改以下配置：

```cpp
#define WIFI_SSID "your_wifi_ssid"        // WiFi 名称
#define WIFI_PASSWORD "your_wifi_password"    // WiFi 密码
// 服务器地址支持以下格式：
// - 纯主机名或 IP: "192.168.1.100" 或 "your-server.local"
// - 带协议: "http://192.168.1.100" 或 "https://your-server.example.com"
#define SERVER_HOST "192.168.1.100"
#define SERVER_PORT 8080                    // 服务器端口（HTTPS 用 443）
#define DEVICE_API_KEY "your_api_key"       // 设备 API Key
```

### 自定义 MAC 地址（可选）

如果需要使用自定义 MAC 地址连接 WiFi，在 `config_local.h` 中添加：

```cpp
#define USE_CUSTOM_MAC_ADDRESS
#define CUSTOM_MAC_ADDRESS_STRING "AA:BB:CC:DD:EE:FF"
```

- 取消注释 `USE_CUSTOM_MAC_ADDRESS` 启用自定义 MAC 功能
- 支持格式: `"AA:BB:CC:DD:EE:FF"` 或 `"AA-BB-CC-DD-EE-FF"`
- 启动时会在串口输出显示当前使用的 MAC 地址（自定义或默认）

或者创建 `include/config_local.h` 文件覆盖默认配置：

```cpp
#ifndef CONFIG_LOCAL_H
#define CONFIG_LOCAL_H

#define WIFI_SSID "your_wifi_ssid"
#define WIFI_PASSWORD "your_wifi_password"
#define SERVER_HOST "your_server_ip"
#define DEVICE_API_KEY "your_device_api_key"

#endif
```

## 硬件连接

| 屏幕引脚 | ESP32-S3 引脚 |
|---------|--------------|
| CS      | GPIO 10      |
| DC      | GPIO 9       |
| RST     | GPIO 8       |
| BUSY    | GPIO 7       |
| MOSI    | GPIO 11      |
| SCK     | GPIO 12      |

## 编译上传

```bash
# 使用 PlatformIO
pio run --target upload

# 或 VS Code + PlatformIO 扩展
# 点击 Upload 按钮
```

## 调试

串口参数: 115200 baud

启动后会输出：
- WiFi 连接状态
- 服务器通信日志
- 显示刷新状态

## 数据格式

屏幕使用 3bit/像素格式表示 6 种颜色：

| 值 | 颜色  |
|---|------|
| 000 | 黑色 |
| 001 | 白色 |
| 010 | 绿色 |
| 011 | 蓝色 |
| 100 | 红色 |
| 101 | 黄色 |

每行 800 像素 = 300 bytes (800 * 3 / 8 = 300)
总缓冲区: 300 * 480 = 144,000 bytes

## 注意事项

1. 首次使用前需要在后端管理界面创建设备并获取 API Key
2. 确保 ESP32-S3 和服务器在同一网络或网络可达
3. 屏幕刷新需要约 10-20 秒，期间不要断电
4. 建议使用稳定的电源供应 (5V/2A)
