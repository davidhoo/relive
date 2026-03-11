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

// E Ink 命令定义（基于官方示例）
#define PSR         0x00
#define PWRR        0x01
#define POF         0x02
#define POFS        0x03
#define PON         0x04
#define BTST1       0x05
#define BTST2       0x06
#define DSLP        0x07
#define BTST3       0x08
#define DTM         0x10
#define DRF         0x12
#define PLL         0x30
#define CDI         0x50
#define TCON        0x60
#define TRES        0x61
#define REV         0x70
#define VDCS        0x82
#define T_VDCS      0x84
#define PWS         0xE3

DisplayDriver::DisplayDriver() : _initialized(false) {}

void DisplayDriver::spiTransfer(uint8_t data) {
    SPI.transfer(data);
}

void DisplayDriver::sendCommand(uint8_t cmd) {
    digitalWrite(EINK_DC, LOW);   // DC=0: command
    digitalWrite(EINK_CS, LOW);
    spiTransfer(cmd);
    digitalWrite(EINK_CS, HIGH);
}

void DisplayDriver::sendData(uint8_t data) {
    digitalWrite(EINK_DC, HIGH);  // DC=1: data
    digitalWrite(EINK_CS, LOW);
    spiTransfer(data);
    digitalWrite(EINK_CS, HIGH);
}

void DisplayDriver::sendData(const uint8_t* data, size_t len) {
    digitalWrite(EINK_DC, HIGH);  // DC=1: data
    digitalWrite(EINK_CS, LOW);
    for (size_t i = 0; i < len; i++) {
        spiTransfer(data[i]);
    }
    digitalWrite(EINK_CS, HIGH);
}

void DisplayDriver::reset() {
    digitalWrite(EINK_RST, LOW);
    delay(10);  // 至少10ms
    digitalWrite(EINK_RST, HIGH);
    delay(10);  // 至少10ms
}

bool DisplayDriver::isBusy() {
    // BUSY引脚：高电平=空闲，低电平=忙碌
    return digitalRead(EINK_BUSY) == LOW;
}

void DisplayDriver::waitUntilIdle() {
    // 等待BUSY引脚变为高电平（空闲状态）
    while (digitalRead(EINK_BUSY) == LOW) {
        delay(10);
    }
}

bool DisplayDriver::begin() {
    DEBUG_SERIAL.println("[Display] 初始化 E Ink Spectra 6 (GDEP073E01)...");

    // 配置引脚
    pinMode(EINK_BUSY, INPUT);
    pinMode(EINK_RST, OUTPUT);
    pinMode(EINK_DC, OUTPUT);
    pinMode(EINK_CS, OUTPUT);

    digitalWrite(EINK_CS, HIGH);
    digitalWrite(EINK_DC, HIGH);
    digitalWrite(EINK_RST, HIGH);

    // 初始化 SPI
    SPI.begin(EINK_SCK, -1, EINK_MOSI, EINK_CS);
    SPI.beginTransaction(SPISettings(10000000, MSBFIRST, SPI_MODE0));

    // 硬件复位
    reset();

    // 使用快速初始化模式
    initFast();

    _initialized = true;
    DEBUG_SERIAL.println("[Display] 初始化完成");
    return true;
}

void DisplayDriver::initFast() {
    DEBUG_SERIAL.println("[Display] 快速初始化序列...");

    // CMDH
    sendCommand(0xAA);
    sendData(0x49);
    sendData(0x55);
    sendData(0x20);
    sendData(0x08);
    sendData(0x09);
    sendData(0x18);

    // PWRR - Power Setting
    sendCommand(PWRR);
    sendData(0x3F);
    sendData(0x00);
    sendData(0x32);
    sendData(0x2A);
    sendData(0x0E);
    sendData(0x2A);

    // PSR - Panel Setting
    sendCommand(PSR);
    sendData(0x5F);
    sendData(0x69);

    // POFS - Power Off Sequence
    sendCommand(POFS);
    sendData(0x00);
    sendData(0x54);
    sendData(0x00);
    sendData(0x44);

    // BTST1 - Booster Soft Start 1
    sendCommand(BTST1);
    sendData(0x40);
    sendData(0x1F);
    sendData(0x1F);
    sendData(0x2C);

    // BTST2 - Booster Soft Start 2
    sendCommand(BTST2);
    sendData(0x6F);
    sendData(0x1F);
    sendData(0x16);
    sendData(0x25);

    // BTST3 - Booster Soft Start 3
    sendCommand(BTST3);
    sendData(0x6F);
    sendData(0x1F);
    sendData(0x1F);
    sendData(0x22);

    // IPC
    sendCommand(0x13);
    sendData(0x00);
    sendData(0x04);

    // PLL - PLL Control
    sendCommand(PLL);
    sendData(0x02);

    // TSE
    sendCommand(0x41);
    sendData(0x00);

    // CDI - VCOM and Data Interval Setting
    sendCommand(CDI);
    sendData(0x3F);

    // TCON - Gate/Source Start Setting
    sendCommand(TCON);
    sendData(0x02);
    sendData(0x00);

    // TRES - Resolution Setting (800x480)
    sendCommand(TRES);
    sendData(0x03);  // 800 >> 8
    sendData(0x20);  // 800 & 0xFF
    sendData(0x01);  // 480 >> 8
    sendData(0xE0);  // 480 & 0xFF

    // VDCS
    sendCommand(VDCS);
    sendData(0x1E);

    // T_VDCS
    sendCommand(T_VDCS);
    sendData(0x01);

    // AGID
    sendCommand(0x86);
    sendData(0x00);

    // PWS - Power Saving
    sendCommand(PWS);
    sendData(0x2F);

    // CCSET
    sendCommand(0xE0);
    sendData(0x00);

    // TSSET
    sendCommand(0xE6);
    sendData(0x00);

    // PWR ON
    sendCommand(0x04);
    waitUntilIdle();

    DEBUG_SERIAL.println("[Display] 快速初始化完成");
}

void DisplayDriver::initNormal() {
    DEBUG_SERIAL.println("[Display] 标准初始化序列...");

    // CMDH
    sendCommand(0xAA);
    sendData(0x49);
    sendData(0x55);
    sendData(0x20);
    sendData(0x08);
    sendData(0x09);
    sendData(0x18);

    // PWRR
    sendCommand(PWRR);
    sendData(0x3F);

    // PSR
    sendCommand(PSR);
    sendData(0x5F);
    sendData(0x69);

    // POFS
    sendCommand(POFS);
    sendData(0x00);
    sendData(0x54);
    sendData(0x00);
    sendData(0x44);

    // BTST1
    sendCommand(BTST1);
    sendData(0x40);
    sendData(0x1F);
    sendData(0x1F);
    sendData(0x2C);

    // BTST2
    sendCommand(BTST2);
    sendData(0x6F);
    sendData(0x1F);
    sendData(0x17);
    sendData(0x49);

    // BTST3
    sendCommand(BTST3);
    sendData(0x6F);
    sendData(0x1F);
    sendData(0x1F);
    sendData(0x22);

    // PLL
    sendCommand(PLL);
    sendData(0x08);

    // CDI
    sendCommand(CDI);
    sendData(0x3F);

    // TCON
    sendCommand(TCON);
    sendData(0x02);
    sendData(0x00);

    // TRES
    sendCommand(TRES);
    sendData(0x03);
    sendData(0x20);
    sendData(0x01);
    sendData(0xE0);

    // T_VDCS
    sendCommand(T_VDCS);
    sendData(0x01);

    // PWS
    sendCommand(PWS);
    sendData(0x2F);

    // PWR ON
    sendCommand(0x04);
    waitUntilIdle();

    DEBUG_SERIAL.println("[Display] 标准初始化完成");
}

void DisplayDriver::clear() {
    if (!_initialized) return;

    DEBUG_SERIAL.println("[Display] 清屏...");

    // 800 * 480 / 2 = 192000 bytes
    const size_t totalBytes = 192000;

    sendCommand(0x10);  // DTM - Data Transmission
    for (size_t i = 0; i < totalBytes; i++) {
        sendData(COLOR_WHITE);  // 0x11 = 白色
    }

    // 刷新
    sendCommand(0x12);  // DRF - Display Refresh
    sendData(0x00);
    delay(1);  // 至少200us
    waitUntilIdle();

    DEBUG_SERIAL.println("[Display] 清屏完成");
}

// 颜色映射函数（从RGB转换为E Ink颜色）
static uint8_t colorGet(uint8_t color) {
    switch(color) {
        case 0x00: return 0x00; // Black
        case 0xFF: return 0x01; // White
        case 0xFC: return 0x02; // Yellow
        case 0xE0: return 0x03; // Red
        case 0x03: return 0x05; // Blue
        case 0x1C: return 0x06; // Green
        default:   return 0x00; // 默认黑色
    }
}

void DisplayDriver::display(const uint8_t* buffer, size_t size) {
    if (!_initialized || buffer == nullptr) return;

    DEBUG_SERIAL.println("[Display] 刷新屏幕...");
    LOG_INFO_F("[Display] 缓冲区大小: %d bytes\n", size);

    // 期望大小：192000 bytes (800 * 480 / 2)
    const size_t expectedSize = 192000;
    if (size != expectedSize) {
        LOG_ERROR_F("[Display] 错误：缓冲区大小不匹配 (期望 %d, 实际 %d)\n", expectedSize, size);
        return;
    }

    // 发送图像数据
    sendCommand(0x10);  // DTM
    
    // 直接发送缓冲区数据（假设已经是4bit格式）
    for (size_t i = 0; i < size; i++) {
        sendData(buffer[i]);
    }

    // 刷新显示
    sendCommand(0x12);  // DRF
    sendData(0x00);
    delay(1);  // 至少200us
    waitUntilIdle();

    DEBUG_SERIAL.println("[Display] 刷新完成");
}

void DisplayDriver::displayRotated(const uint8_t* srcBuffer, size_t size) {
    if (!_initialized || srcBuffer == nullptr) return;

    DEBUG_SERIAL.println("[Display] 旋转显示竖屏图片...");

    // 源图片：480x800，4bit格式
    const int SRC_WIDTH = 480;
    const int SRC_HEIGHT = 800;
    const int DST_WIDTH = 800;
    const int DST_HEIGHT = 480;

    // 期望源大小：480 * 800 / 2 = 192000 bytes
    const size_t expectedSize = 192000;
    if (size != expectedSize) {
        LOG_ERROR_F("[Display] 错误：源缓冲区大小不匹配 (期望 %d, 实际 %d)\n", expectedSize, size);
        return;
    }

    // 分配目标缓冲区
    const size_t dstSize = 192000;  // 800 * 480 / 2
    uint8_t* rotatedBuffer = (uint8_t*)ps_malloc(dstSize);
    if (rotatedBuffer == nullptr) {
        rotatedBuffer = (uint8_t*)malloc(dstSize);
    }
    if (rotatedBuffer == nullptr) {
        DEBUG_SERIAL.println("[Display] 旋转缓冲区分配失败");
        return;
    }

    // 初始化为白色
    memset(rotatedBuffer, COLOR_WHITE, dstSize);

    DEBUG_SERIAL.println("[Display] 开始旋转...");

    // 旋转90度：源(x, y) -> 目标(799-y, x)
    // 4bit格式：每字节包含2个像素（高4位和低4位）
    for (int srcY = 0; srcY < SRC_HEIGHT; srcY++) {
        for (int srcX = 0; srcX < SRC_WIDTH; srcX++) {
            // 读取源像素
            int srcPixelIndex = srcY * SRC_WIDTH + srcX;
            int srcByteIndex = srcPixelIndex / 2;
            int srcBitOffset = (srcPixelIndex % 2) * 4;
            uint8_t srcColor = (srcBuffer[srcByteIndex] >> srcBitOffset) & 0x0F;

            // 计算目标位置：旋转90度顺时针
            int dstX = SRC_HEIGHT - 1 - srcY;  // 799 - srcY
            int dstY = srcX;

            // 写入目标像素
            int dstPixelIndex = dstY * DST_WIDTH + dstX;
            int dstByteIndex = dstPixelIndex / 2;
            int dstBitOffset = (dstPixelIndex % 2) * 4;

            // 清除目标位置的4位，然后设置新颜色
            rotatedBuffer[dstByteIndex] &= ~(0x0F << dstBitOffset);
            rotatedBuffer[dstByteIndex] |= (srcColor << dstBitOffset);
        }
    }

    DEBUG_SERIAL.println("[Display] 旋转完成，发送到屏幕...");

    // 发送旋转后的数据
    sendCommand(0x10);  // DTM
    for (size_t i = 0; i < dstSize; i++) {
        sendData(rotatedBuffer[i]);
    }

    // 刷新显示
    sendCommand(0x12);  // DRF
    sendData(0x00);
    delay(1);
    waitUntilIdle();

    // 释放缓冲区
    free(rotatedBuffer);

    DEBUG_SERIAL.println("[Display] 旋转显示完成");
}

void DisplayDriver::sleep() {
    if (!_initialized) return;

    DEBUG_SERIAL.println("[Display] 进入深度睡眠...");

    sendCommand(0x02);  // POF - Power Off
    sendData(0x00);
    waitUntilIdle();

    // 可选：进入深度睡眠模式
    // sendCommand(0x07);  // DSLP
    // sendData(0xA5);

    delay(100);
}

void DisplayDriver::wakeup() {
    DEBUG_SERIAL.println("[Display] 唤醒...");

    // 硬件复位
    reset();
    delay(100);

    // 重新初始化
    begin();
}
