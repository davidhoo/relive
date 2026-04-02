# 踩坑经验 (Gotchas)

开发过程中遇到的陷阱和注意事项，避免重复踩坑。

## GORM + SQLite

- **`Order()` SQL 注入**：GORM `Order()` 不做参数化，用户输入的排序字段必须白名单校验
- **FindInBatches 反引号**：对匿名结构体会生成反引号包裹的 SQL，SQLite 不兼容，需手动分页替代
- **自定义结构体查询**：用 `db.Table("table_name").Scan(&results)` 而非 `db.Model(&Struct{}).Find(&results)`
- **CHECK 约束空值**：枚举字段 release 时不能设为空字符串（违反 CHECK），如 `AnalysisRuntimeLease.owner_type` 须设为 `"idle"`
- **FTS5 语法冲突**：`buildFTSQuery` 用空格分词 + 双引号包裹每个词，防止用户输入触发 FTS5 操作符

## Go 后端

- **路径安全**：`filepath.Join(base, filepath.Clean(userInput))` 是安全的，`..` 不会逃逸 base 目录
- **Gin 类型断言**：`c.Get()` 返回值做类型断言时必须用 comma-ok 模式 `v, ok := val.(Type)`，避免 nil panic

## Vue 前端

- **定时器泄漏**：`setInterval` / `setTimeout` 必须在 `onBeforeUnmount` 中清理，否则组件卸载后继续执行
- **401/403 重定向**：用 Vue Router 动态 `import()` 跳转（避免循环依赖），不用 `window.location.href`

## ESP32 硬件

- **S3 串口**：CH340/CP2102 等外置 USB 转串口芯片的板子必须 `CDC_ON_BOOT=0`，否则 `Serial` 走原生 USB CDC 而非 UART0，串口监视器无输出，易误判为 boot loop
- **S3 PSRAM**：PSRAM 初始化失败会在 `setup()` 之前 crash，排查时先禁用 PSRAM 确认基本功能正常，再逐步加回
- **墨水屏 BUSY**：`waitUntilIdle()` 必须有超时保护，BUSY 引脚异常时无限循环会触发看门狗重启
