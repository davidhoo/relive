#include "display_driver.h"

#if LOG_LEVEL >= 2
#define LOG_INFO(msg) DEBUG_SERIAL.println(msg)
#define LOG_INFO_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_INFO(msg)
#define LOG_INFO_F(msg, ...)
#endif

#if LOG_LEVEL >= 3
#define LOG_DEBUG(msg) DEBUG_SERIAL.println(msg)
#define LOG_DEBUG_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_DEBUG(msg)
#define LOG_DEBUG_F(msg, ...)
#endif

#if LOG_LEVEL >= 1
#define LOG_ERROR(msg) DEBUG_SERIAL.println(msg)
#define LOG_ERROR_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_ERROR(msg)
#define LOG_ERROR_F(msg, ...)
#endif

// E Ink Spectra 6 命令定义
#define CMD_PANEL_SETTING 0x00
#define CMD_POWER_SETTING 0x01
#define CMD_POWER_OFF 0x02
#define CMD_POWER_ON 0x04
#define CMD_BOOSTER_SOFT_START 0x06
#define CMD_DEEP_SLEEP 0x07
#define CMD_DISPLAY_START_TRANSMISSION 0x10
#define CMD_DISPLAY_REFRESH 0x12
#define CMD_VCOM_AND_DATA_INTERVAL_SETTING 0x50
#define CMD_RESOLUTION_SETTING 0x61
#define CMD_GET_STATUS 0x71

DisplayDriver::DisplayDriver() : _initialized(false) {}

void DisplayDriver::spiTransfer(uint8_t data) {
    SPI.transfer(data);
}

void DisplayDriver::sendCommand(uint8_t cmd) {
    digitalWrite(EINK_DC, LOW);
    digitalWrite(EINK_CS, LOW);
    spiTransfer(cmd);
    digitalWrite(EINK_CS, HIGH);
}

void DisplayDriver::sendData(uint8_t data) {
    digitalWrite(EINK_DC, HIGH);
    digitalWrite(EINK_CS, LOW);
    spiTransfer(data);
    digitalWrite(EINK_CS, HIGH);
}

void DisplayDriver::sendData(const uint8_t* data, size_t len) {
    digitalWrite(EINK_DC, HIGH);
    digitalWrite(EINK_CS, LOW);
    for (size_t i = 0; i < len; i++) {
        spiTransfer(data[i]);
    }
    digitalWrite(EINK_CS, HIGH);
}

void DisplayDriver::reset() {
    digitalWrite(EINK_RST, HIGH);
    delay(20);
    digitalWrite(EINK_RST, LOW);
    delay(2);
    digitalWrite(EINK_RST, HIGH);
    delay(20);
}

bool DisplayDriver::isBusy() {
    return digitalRead(EINK_BUSY) == LOW;
}

void DisplayDriver::waitUntilIdle() {
    // 忙信号低电平有效
    while (digitalRead(EINK_BUSY) == LOW) {
        delay(10);
    }
    delay(100); // 额外延迟确保稳定
}

bool DisplayDriver::begin() {
    DEBUG_SERIAL.println("[Display] 初始化 E Ink Spectra 6...");

    // 配置引脚
    pinMode(EINK_CS, OUTPUT);
    pinMode(EINK_DC, OUTPUT);
    pinMode(EINK_RST, OUTPUT);
    pinMode(EINK_BUSY, INPUT_PULLUP);

    digitalWrite(EINK_CS, HIGH);
    digitalWrite(EINK_DC, HIGH);

    // 初始化 SPI
    SPI.begin(EINK_SCK, -1, EINK_MOSI, EINK_CS);
    SPI.setFrequency(20000000); // 20MHz

    // 复位屏幕
    reset();

    // 等待屏幕就绪
    waitUntilIdle();

    // 软启动
    sendCommand(CMD_BOOSTER_SOFT_START);
    sendData(0x17);
    sendData(0x17);
    sendData(0x17);
    delay(10);

    // 电源设置
    sendCommand(CMD_POWER_SETTING);
    sendData(0x07);
    sendData(0x17);
    sendData(0x3F);
    sendData(0x3F);
    sendData(0x0D);
    delay(10);

    // 面板设置
    sendCommand(CMD_PANEL_SETTING);
    sendData(0x0F); // 默认设置
    delay(10);

    // 设置分辨率 800x480
    sendCommand(CMD_RESOLUTION_SETTING);
    sendData(0x03); // 800 >> 8
    sendData(0x20); // 800 & 0xFF
    sendData(0x01); // 480 >> 8
    sendData(0xE0); // 480 & 0xFF

    // VCOM 和数据间隔设置
    sendCommand(CMD_VCOM_AND_DATA_INTERVAL_SETTING);
    sendData(0x31);
    sendData(0x07);

    _initialized = true;
    DEBUG_SERIAL.println("[Display] 初始化完成");
    return true;
}

void DisplayDriver::clear() {
    if (!_initialized) return;

    DEBUG_SERIAL.println("[Display] 清屏...");

    // 计算每行字节数: 800 * 3bit / 8 = 300 bytes
    const int bytesPerLine = (SCREEN_WIDTH * 3 + 7) / 8; // 300 bytes

    // 白色像素在 Spectra 6 中通常用 0x01 表示 (3bit: 001)
    // 每 8 个像素 = 3 bytes，全部为 0x49 (01001001) 表示全白
    uint8_t whiteLine[300];
    memset(whiteLine, 0x49, sizeof(whiteLine));

    sendCommand(CMD_DISPLAY_START_TRANSMISSION);

    for (int y = 0; y < SCREEN_HEIGHT; y++) {
        sendData(whiteLine, bytesPerLine);
    }

    sendCommand(CMD_DISPLAY_REFRESH);
    waitUntilIdle();

    DEBUG_SERIAL.println("[Display] 清屏完成");
}

void DisplayDriver::display(const uint8_t* buffer) {
    if (!_initialized || buffer == nullptr) return;

    DEBUG_SERIAL.println("[Display] 刷新屏幕...");

    const int bytesPerLine = (SCREEN_WIDTH * 3 + 7) / 8; // 300 bytes
    const size_t totalBytes = bytesPerLine * SCREEN_HEIGHT;

    // 上电
    sendCommand(CMD_POWER_ON);
    waitUntilIdle();

    // 发送图像数据
    sendCommand(CMD_DISPLAY_START_TRANSMISSION);
    sendData(buffer, totalBytes);

    // 刷新显示
    sendCommand(CMD_DISPLAY_REFRESH);
    waitUntilIdle();

    // 断电
    sendCommand(CMD_POWER_OFF);
    waitUntilIdle();

    DEBUG_SERIAL.println("[Display] 刷新完成");
}

// 辅助函数：从源缓冲区获取指定坐标的像素值（3bit）
static inline uint8_t getPixel(const uint8_t* buffer, int x, int y, int srcWidth) {
    int pixelIndex = y * srcWidth + x;
    int byteIndex = pixelIndex * 3 / 8;
    int bitOffset = (pixelIndex * 3) % 8;

    uint16_t value = buffer[byteIndex] | (buffer[byteIndex + 1] << 8);
    return (value >> bitOffset) & 0x07;
}

// 辅助函数：设置目标缓冲区的像素值（3bit）
static inline void setPixel(uint8_t* buffer, int x, int y, int dstWidth, uint8_t color) {
    int pixelIndex = y * dstWidth + x;
    int byteIndex = pixelIndex * 3 / 8;
    int bitOffset = (pixelIndex * 3) % 8;

    uint16_t mask = ~(0x07 << bitOffset);
    uint16_t value = (color & 0x07) << bitOffset;
    uint16_t current = buffer[byteIndex] | (buffer[byteIndex + 1] << 8);
    current = (current & mask) | value;
    buffer[byteIndex] = current & 0xFF;
    buffer[byteIndex + 1] = (current >> 8) & 0xFF;
}

void DisplayDriver::displayRotated(const uint8_t* srcBuffer) {
    if (!_initialized || srcBuffer == nullptr) return;

    DEBUG_SERIAL.println("[Display] 旋转显示竖屏图片...");

    // 源图片：480x800
    const int SRC_WIDTH = 480;
    const int SRC_HEIGHT = 800;
    const int DST_WIDTH = SCREEN_WIDTH;   // 800
    const int DST_HEIGHT = SCREEN_HEIGHT; // 480

    // 计算每行字节数
    const int dstBytesPerLine = (DST_WIDTH * 3 + 7) / 8; // 300 bytes
    const size_t dstTotalBytes = dstBytesPerLine * DST_HEIGHT;

    // 分配临时缓冲区用于旋转后的数据
    uint8_t* rotatedBuffer = (uint8_t*)ps_malloc(dstTotalBytes);
    if (rotatedBuffer == nullptr) {
        rotatedBuffer = (uint8_t*)malloc(dstTotalBytes);
    }
    if (rotatedBuffer == nullptr) {
        DEBUG_SERIAL.println("[Display] 旋转缓冲区分配失败");
        return;
    }

    // 清空缓冲区（白色）
    memset(rotatedBuffer, 0x49, dstTotalBytes);

    DEBUG_SERIAL.println("[Display] 开始旋转...");

    // 旋转 90 度：源(x, y) -> 目标(y, 479-x)
    // 同时需要缩放/裁剪以适应屏幕
    // 简单方案：居中显示，保持比例

    // 计算缩放比例和偏移量
    // 源高 800 映射到目标宽 800（1:1）
    // 源宽 480 映射到目标高 480（1:1）
    // 完美匹配！

    for (int dstY = 0; dstY < DST_HEIGHT; dstY++) {
        for (int dstX = 0; dstX < DST_WIDTH; dstX++) {
            // 目标(dstX, dstY) 对应 源(479-dstY, dstX)
            int srcX = 479 - dstY;
            int srcY = dstX;

            if (srcX >= 0 && srcX < SRC_WIDTH && srcY >= 0 && srcY < SRC_HEIGHT) {
                uint8_t color = getPixel(srcBuffer, srcX, srcY, SRC_WIDTH);
                setPixel(rotatedBuffer, dstX, dstY, DST_WIDTH, color);
            }
        }
    }

    DEBUG_SERIAL.println("[Display] 旋转完成，刷新屏幕...");

    // 上电
    sendCommand(CMD_POWER_ON);
    waitUntilIdle();

    // 发送图像数据
    sendCommand(CMD_DISPLAY_START_TRANSMISSION);
    sendData(rotatedBuffer, dstTotalBytes);

    // 刷新显示
    sendCommand(CMD_DISPLAY_REFRESH);
    waitUntilIdle();

    // 断电
    sendCommand(CMD_POWER_OFF);
    waitUntilIdle();

    // 释放缓冲区
    free(rotatedBuffer);

    DEBUG_SERIAL.println("[Display] 刷新完成");
}

void DisplayDriver::sleep() {
    if (!_initialized) return;

    DEBUG_SERIAL.println("[Display] 进入深度睡眠...");

    sendCommand(CMD_DEEP_SLEEP);
    sendData(0xA5); // 需要这个确认码

    delay(100);
}

void DisplayDriver::wakeup() {
    DEBUG_SERIAL.println("[Display] 唤醒...");

    // 复位唤醒
    reset();
    delay(100);

    // 重新初始化
    begin();
}
