# ESP32 到 Device 重构总结

> 日期：2026-03-04
> 类型：架构重构
> 影响范围：数据库、Service、API、文档

---

## 📋 重构目标

将硬件实现细节（ESP32）从系统架构中抽象出来，改为通用的业务概念（Device/设备），以支持未来多种硬件平台。

### 背景

- **当前问题**：系统命名（esp32_devices、ESP32Handler）与具体硬件绑定
- **未来需求**：支持多种硬件平台（ESP32、ESP8266、STM32、Android、iOS、Web）
- **解决方案**：使用通用的 "Device" 概念，通过 `device_type` 字段区分硬件类型

---

## 🎯 重构内容

### 1. 数据库层

#### 表结构变更

| 变更类型 | 旧名称 | 新名称 | 说明 |
|---------|--------|--------|------|
| 表名 | `esp32_devices` | `devices` | 重命名表 |
| 字段 | - | `device_type` | 新增：设备类型（esp32/android/ios等） |
| 字段 | - | `hardware_model` | 新增：硬件型号（ESP32-S3/iPhone 15等） |
| 字段 | - | `platform` | 新增：平台类型（embedded/mobile/web） |

#### 自动迁移

```sql
-- 自动重命名旧表
ALTER TABLE esp32_devices RENAME TO devices;

-- 添加新字段（带默认值）
ALTER TABLE devices ADD COLUMN device_type VARCHAR(20) DEFAULT 'esp32';
ALTER TABLE devices ADD COLUMN hardware_model VARCHAR(50);
ALTER TABLE devices ADD COLUMN platform VARCHAR(20) DEFAULT 'embedded';
```

**迁移策略**：
- ✅ 新部署：直接创建 `devices` 表
- ✅ 已有部署：自动检测并迁移 `esp32_devices` → `devices`
- ✅ 兼容性：保留 `ESP32Device` 类型别名

---

### 2. Model 层

```go
// 新模型
type Device struct {
    // ... 原有字段
    DeviceType    string  // 设备类型
    HardwareModel string  // 硬件型号
    Platform      string  // 平台类型
}

// 向后兼容
type ESP32Device = Device
```

---

### 3. Repository 层

#### 文件重命名

- `esp32_device_repo.go` → `device_repo.go`

#### 接口变更

```go
// 新接口
type DeviceRepository interface {
    // ... 原有方法
    ListByDeviceType(deviceType string) ([]*Device, error)
    ListByPlatform(platform string) ([]*Device, error)
    CountByDeviceType(deviceType string) (int64, error)
    CountByPlatform(platform string) (int64, error)
}

// 向后兼容
type ESP32DeviceRepository = DeviceRepository
```

---

### 4. Service 层

#### 文件重命名

- `esp32_service.go` → `device_service.go`

#### 接口变更

```go
// 新接口
type DeviceService interface {
    Register(req *DeviceRegisterRequest) (*DeviceRegisterResponse, error)
    // ... 其他方法
}

// 向后兼容
type ESP32Service = DeviceService
```

---

### 5. Handler/API 层

#### 文件重命名

- `esp32_handler.go` → `device_handler.go`

#### API 路由

| 类型 | 路径 | 说明 |
|------|------|------|
| **新路径（推荐）** | `/api/v1/devices/*` | 通用设备 API |
| **旧路径（兼容）** | `/api/v1/esp32/*` | ESP32 专用 API（deprecated） |

**路由映射**：

```go
// 新路径
POST /api/v1/devices/register
POST /api/v1/devices/heartbeat
GET  /api/v1/devices
GET  /api/v1/devices/stats
GET  /api/v1/devices/:id

// 旧路径（仍可用）
POST /api/v1/esp32/register      → 指向 DeviceHandler
POST /api/v1/esp32/heartbeat     → 指向 DeviceHandler
GET  /api/v1/esp32/devices       → 指向 DeviceHandler
GET  /api/v1/esp32/stats         → 指向 DeviceHandler
GET  /api/v1/esp32/devices/:id   → 指向 DeviceHandler
```

---

### 6. DTO 层

```go
// 新 DTO
type DeviceRegisterRequest struct {
    DeviceID      string `json:"device_id"`
    Name          string `json:"name"`
    DeviceType    string `json:"device_type"`    // 新增
    HardwareModel string `json:"hardware_model"` // 新增
    Platform      string `json:"platform"`       // 新增
    // ... 其他字段
}

// 向后兼容
type ESP32RegisterRequest = DeviceRegisterRequest
```

---

### 7. 文档更新

| 文件 | 变更 |
|------|------|
| `ESP32_PROTOCOL.md` | 重命名为 `DEVICE_PROTOCOL.md` |
| `ARCHITECTURE.md` | 更新架构图和说明 |
| `README.md` | 更新项目描述 |
| `CLAUDE.md` | 更新项目概述 |

---

## ✅ 向后兼容性

### 代码层面

所有旧代码无需修改，通过类型别名保持兼容：

```go
// 旧代码仍然有效
var device *model.ESP32Device
service := service.NewESP32Service(repo, cfg)
handler := handler.NewESP32Handler(service)
```

### API 层面

所有旧 API 路径仍然可用：

```bash
# ESP32 固件无需修改
POST /api/v1/esp32/register      # ✅ 仍然有效
POST /api/v1/esp32/heartbeat     # ✅ 仍然有效
```

### 数据库层面

自动迁移，无需手动操作：

- 检测 `esp32_devices` 表 → 自动重命名为 `devices`
- 自动添加新字段（带默认值）

---

## 🚀 新功能

### 1. 多设备类型支持

```json
{
  "device_id": "ANDROID-001",
  "name": "我的手机",
  "device_type": "android",
  "hardware_model": "Pixel 8",
  "platform": "mobile",
  "screen_width": 1080,
  "screen_height": 2400
}
```

### 2. 按类型筛选

```bash
# 获取所有 ESP32 设备
GET /api/v1/devices?device_type=esp32

# 获取所有 Android 设备
GET /api/v1/devices?device_type=android

# 获取所有移动设备
GET /api/v1/devices?platform=mobile
```

### 3. 增强的统计

```json
{
  "total": 10,
  "online": 5,
  "by_type": {
    "esp32": 7,
    "android": 2,
    "ios": 1
  },
  "by_platform": {
    "embedded": 7,
    "mobile": 3
  }
}
```

---

## 📝 迁移指南

### 对于开发者

#### 新代码（推荐）

```go
// 使用新命名
import "github.com/davidhoo/relive/internal/model"

device := &model.Device{
    DeviceID:      "DEVICE-001",
    Name:          "客厅相框",
    DeviceType:    "esp32",
    HardwareModel: "ESP32-S3",
    Platform:      "embedded",
}
```

#### 旧代码（仍可用）

```go
// 仍然有效，自动映射到 Device
device := &model.ESP32Device{
    DeviceID: "ESP32-001",
    Name:     "客厅相框",
}
```

### 对于 ESP32 固件开发者

**无需任何修改**，现有固件代码完全兼容：

```cpp
// 现有代码仍然有效
POST /api/v1/esp32/register
POST /api/v1/esp32/heartbeat
```

### 对于新硬件平台

```json
// Android 设备注册示例
POST /api/v1/devices/register
{
  "device_id": "ANDROID-UUID",
  "name": "My Phone",
  "device_type": "android",
  "hardware_model": "Pixel 8",
  "platform": "mobile",
  "screen_width": 1080,
  "screen_height": 2400
}
```

---

## 🎉 重构成果

### ✅ 完成项

- [x] 数据库模型重构
- [x] Repository 层重构
- [x] Service 层重构
- [x] Handler/API 层重构
- [x] DTO 层重构
- [x] 文档更新
- [x] 向后兼容保证

### ✅ 验证项

- [x] 编译通过
- [x] 现有 API 保持可用
- [x] 数据库自动迁移
- [x] 类型别名正常工作

---

## 📚 相关文档

- [DEVICE_PROTOCOL.md](./DEVICE_PROTOCOL.md) - 设备通信协议（已更新）
- [ARCHITECTURE.md](./ARCHITECTURE.md) - 系统架构文档
- [DATABASE_SCHEMA.md](./DATABASE_SCHEMA.md) - 数据库结构

---

## 🔄 未来扩展

支持的设备类型（可扩展）：

| 设备类型 | device_type | platform | 说明 |
|---------|-------------|----------|------|
| ESP32 | `esp32` | `embedded` | 现有支持 |
| ESP8266 | `esp8266` | `embedded` | 可扩展 |
| STM32 | `stm32` | `embedded` | 可扩展 |
| Android | `android` | `mobile` | 可扩展 |
| iOS | `ios` | `mobile` | 可扩展 |
| Web | `web` | `web` | 可扩展 |
| Raspberry Pi | `raspberry_pi` | `embedded` | 可扩展 |

---

**重构完成时间**：2026-03-04
**测试状态**：✅ 编译通过，API 兼容
**部署影响**：✅ 自动迁移，无需手动干预
