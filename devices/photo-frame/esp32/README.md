# Relive Photo Frame for ESP32

该目录用于存放 `photo-frame` 设备在 ESP32 平台上的固件实现。

## 概述

ESP32 固件负责驱动墨水屏相框，从 Relive 后端获取照片并展示。

与旧的顶层 `esp32/` 目录相比，这里只描述 **ESP32 平台特有** 的内容；
设备通用协议已经统一收敛到 `../protocol/` 和 `../../../docs/DEVICE_PROTOCOL.md`。

## 硬件要求

- **主控**：ESP32-S3（PSRAM ≥ 384KB）
- **显示屏**：7.3 寸彩色墨水屏 GDEP073E01
- **电源**：2×18650 锂电池（可选）或 USB-C 供电

## 开发环境

### 使用 PlatformIO

```bash
# 安装 PlatformIO
pip install platformio

# 初始化项目
cd devices/photo-frame/esp32
pio init --board esp32-s3-devkitc-1

# 编译
pio run

# 上传
pio run --target upload

# 串口监控
pio device monitor
```

## 计划中的项目结构

```text
devices/photo-frame/esp32/
├── src/
│   └── main.cpp              # 主程序入口
├── lib/
│   ├── display/              # 墨水屏驱动
│   ├── network/              # 网络通信
│   ├── power/                # 电源管理
│   └── config/               # 配置管理
├── include/
│   └── config.h              # 配置头文件
├── platformio.ini            # PlatformIO 配置
└── README.md                 # 本文件
```

## 平台职责

### 1. 硬件初始化
- ESP32-S3 配置
- PSRAM 初始化
- WiFi 配置
- 时间同步（NTP）

### 2. 墨水屏驱动
- 7.3 寸彩色墨水屏初始化
- 图片渲染和显示
- 局部刷新（可选）

### 3. 设备通信接入
- 设备激活 / 注册
- 心跳上报
- 获取照片
- 记录展示

### 4. 电源管理
- 深度睡眠模式
- 定时唤醒（每天 8:00）
- 低电量保护

### 5. 用户交互
- 按钮手动刷新
- LED 状态指示

## 协议与接口

- 通用设备协议：`../../../docs/DEVICE_PROTOCOL.md`
- 平台目录不再单独维护一份 ESP32 专属协议副本

当前建议优先以以下接口口径对齐：

- 获取展示信息：`GET /api/v1/device/display`
- 获取展示二进制：`GET /api/v1/device/display.bin`
- 获取照片信息：`GET /api/v1/display/photo`
- 记录展示：`POST /api/v1/display/record`

设备通过后台预分配的 `api_key` 接入，不再使用注册、激活、心跳流程。

## 配置

### WiFi 配置

首次使用时，设备会创建 AP（热点）：
- SSID: `Relive-XXXXXX`
- Password: `12345678`

连接后访问 `http://192.168.4.1` 配置 WiFi。

### API 配置

在配置页面设置：
- API 地址：`http://192.168.1.100:8080`
- API Key：从 Relive 后端获取

## 开发计划

### Phase 1：基础功能（1 周）
- [ ] PlatformIO 工程初始化
- [ ] WiFi 连接管理
- [ ] HTTP 客户端封装
- [ ] 设备注册和心跳

### Phase 2：显示功能（3-4 天）
- [ ] 墨水屏驱动集成
- [ ] 图片下载和缓存
- [ ] 图片渲染和显示
- [ ] 错误处理和状态显示

### Phase 3：电源管理（2-3 天）
- [ ] 深度睡眠实现
- [ ] 定时唤醒
- [ ] 电池电量监控
- [ ] 低电量保护

### Phase 4：优化和测试（2-3 天）
- [ ] 性能优化
- [ ] 稳定性测试
- [ ] 内存优化
- [ ] OTA 固件升级（可选）

## 依赖库

```ini
[env:esp32-s3-devkitc-1]
platform = espressif32
board = esp32-s3-devkitc-1
framework = arduino
lib_deps =
    GxEPD2
    ArduinoJson
    WiFiManager
```

## 参考资源

- `../../../docs/DEVICE_PROTOCOL.md` - 设备通信协议
- <https://github.com/dai-hongtao/InkTime> - 参考实现
- <https://github.com/ZinggJM/GxEPD2> - 墨水屏驱动库

## 故障排除

### WiFi 连接失败
- 检查 WiFi 配置是否正确
- 确认信号强度
- 重启设备重新配置

### 显示异常
- 检查墨水屏连接
- 确认 PSRAM 足够
- 查看串口日志

### API 通信失败
- 检查 API 地址配置
- 确认网络连接
- 验证 API Key
