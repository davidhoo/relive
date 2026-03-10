# Relive ESP32 墨水屏相框固件

基于 ESP32-S3 的简化版墨水屏数字相框固件，专为 Relive 照片记忆系统设计。

## 核心特性

- **简化接口设计**：只请求一个接口 `/api/v1/device/display.bin` 直接获取照片 bin 文件
- **低功耗设计**：深度睡眠模式，定时唤醒刷新
- **自动重试**：网络错误自动重试机制（最多3次）
- **校验和验证**：SHA256 校验确保数据传输完整性
- **硬件抽象**：通过抽象层支持多种墨水屏驱动

## 硬件要求

- **主控**：ESP32-S3（推荐 8MB Flash + 2MB PSRAM）
- **墨水屏**：支持 480x800 分辨率
  - 测试通过：GDEP073E01 (7.3寸 彩色)
  - 兼容：其他 SPI 接口 800x480 墨水屏
- **网络**：Wi-Fi 2.4GHz
- **电源**：5V/2A 或锂电池+充电模块

## 项目结构

```
devices/photo-frame/esp32/
├── include/
│   ├── config.h                    # 主配置（从 config_local.h 加载敏感信息）
│   ├── config_local.h.example      # 本地配置模板
│   ├── wifi_manager.h              # WiFi 管理
│   ├── api_client.h                # API 客户端
│   └── display_driver.h            # 显示驱动抽象层
├── lib/
│   ├── wifi_manager.cpp            # WiFi 实现
│   ├── api_client.cpp              # API 客户端实现
│   └── display_driver.cpp          # 显示驱动实现
├── src/
│   └── main.cpp                    # 主程序
├── platformio.ini                  # PlatformIO 配置
└── README.md                       # 本文件
```

## 快速开始

### 1. 硬件连接

```
ESP32-S3          墨水屏
--------          ------
GPIO10  (CS)  ->  CS
GPIO11  (SCK) ->  SCK
GPIO12  (MOSI)->  SDI
GPIO13  (DC)  ->  DC
GPIO14  (RST) ->  RST
GPIO15  (BUSY)->  BUSY
GPIO2   (LED) ->  状态指示灯（可选）
GPIO0   (BTN) ->  手动刷新按钮（可选，接地触发）
3.3V          ->  VCC
GND           ->  GND
```

### 2. 配置

```bash
# 进入项目目录
cd devices/photo-frame/esp32

# 复制配置文件模板
cp include/config_local.h.example include/config_local.h

# 编辑配置
nano include/config_local.h
```

填入你的配置：

```cpp
#define WIFI_SSID       "your-wifi-ssid"
#define WIFI_PASSWORD   "your-wifi-password"
#define API_BASE_URL    "http://your-server:8080/api/v1"
#define DEVICE_API_KEY  "sk-relive-your-device-api-key"
```

### 3. 编译烧录

```bash
# 安装 PlatformIO
pip install platformio

# 编译
pio run

# 烧录并监控
pio run --target upload
pio device monitor
```

## API 接口

### 获取照片 Bin 文件

```
GET /api/v1/device/display.bin
Headers:
  X-API-Key: <device-api-key>

Response:
  Content-Type: application/octet-stream
  X-Asset-ID: <asset-id>
  X-Checksum: <sha256-hex>
  X-Photo-ID: <photo-id>
  X-Render-Profile: <profile-name>
  X-Batch-Date: <YYYY-MM-DD>
  X-Sequence: <sequence-number>

  [bin file data]
```

### Bin 文件格式

```
[0-3]   "RLVD"          - 魔数 (4 bytes)
[4]     version         - 版本 (1 byte, = 1)
[5]     palette_colors  - 调色板颜色数 (1 byte)
[6]     dither_mode_len - 抖动模式字符串长度 (1 byte)
[7]     reserved        - 保留 (1 byte)
[8-9]   width           - 宽度 (uint16, little-endian)
[10-11] height          - 高度 (uint16, little-endian)
[12..]  dither_mode     - 抖动模式字符串
[...]   pixel_data      - 调色板索引数据 (每个像素 1 byte)
```

## 工作流程

```
启动
  |
  v
初始化显示
  |
  v
连接 WiFi
  |
  v
GET /device/display.bin
  |
  v
验证校验和
  |
  v
显示照片
  |
  v
进入深度睡眠（1小时）
  |
  v
定时唤醒 -> 重复以上流程
```

## 状态指示

| LED 状态 | 含义 |
|---------|------|
| 单次慢闪 | 连接 WiFi 中 |
| 双闪 | 下载数据中 |
| 缓慢闪烁 | 刷新显示中 |
| 快速闪烁 x5 | 错误发生，即将重启 |

## 调试

```bash
# 监控串口输出
pio device monitor

# 调整日志级别（platformio.ini）
build_flags = -DLOG_LEVEL=4  # DEBUG 级别
```

## 日志级别

- `0`: 无日志
- `1`: ERROR
- `2`: WARN
- `3`: INFO (默认)
- `4`: DEBUG

## 故障排除

### WiFi 连接失败
- 检查 SSID 和密码
- 确认 WiFi 是 2.4GHz
- 检查信号强度

### 显示异常
- 检查屏幕连接线
- 确认屏幕型号和驱动匹配
- 查看串口日志

### API 通信失败
- 检查 API_BASE_URL 格式（需要 http:// 前缀）
- 确认 DEVICE_API_KEY 正确
- 检查服务器防火墙设置

## 许可证

MIT License
