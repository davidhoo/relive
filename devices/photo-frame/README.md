# Photo Frame Device

`photo-frame` 表示 Relive 的墨水屏相册设备类型。

目录按“通用能力 + 平台实现”划分：

```text
devices/photo-frame/
├── README.md          # 设备总览
├── protocol/          # 设备协议与接口入口
├── common/            # 跨平台共享约定与资源
└── esp32/             # ESP32 平台实现
```

## 目录说明

- `protocol/`：指向设备通信协议、接口约定、示例请求等通用内容
- `common/`：放渲染规格、测试数据、共享脚本、配置模板等跨平台内容
- `esp32/`：当前优先实现的平台，后续可并列扩展 `esp8266/`、`stm32/`、`raspberry-pi/`

## 当前状态

- 设备协议主文档：`../../docs/DEVICE_PROTOCOL.md`
- 当前仅完成目录迁移与文档重组
- 具体固件工程文件尚未初始化
