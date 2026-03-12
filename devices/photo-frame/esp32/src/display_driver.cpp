#include "display_driver.h"
#include "log.h"
#include <Adafruit_GFX.h>

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

// 将竖屏 GFXcanvas1 (480x800, 1-bit) 旋转 90° 顺时针映射到物理屏幕 (800x480, 4-bit) 并刷新
// 白底黑字：GFX color 1 (text) → EINK_BLACK, GFX color 0 (background) → EINK_WHITE
// 旋转映射：canvas(srcX, srcY) → screen(799-srcY, srcX)
void DisplayDriver::renderCanvasToDisplay(uint8_t* canvas1Buf, int canvasW, int canvasH) {
    // canvasW=480 (竖屏宽), canvasH=800 (竖屏高)
    // 屏幕: SCREEN_WIDTH=800, SCREEN_HEIGHT=480
    const int rowStride = (canvasW + 7) / 8; // canvas 每行字节数

    sendCommand(0x10); // DTM
    for (int screenY = 0; screenY < SCREEN_HEIGHT; screenY++) {
        for (int screenX = 0; screenX < SCREEN_WIDTH; screenX += 2) {
            uint8_t p0 = EINK_WHITE; // 默认白色背景
            uint8_t p1 = EINK_WHITE;

            // 反向映射: screen(screenX, screenY) ← canvas(srcX=screenY, srcY=799-screenX)
            {
                int srcX = screenY;
                int srcY = (canvasH - 1) - screenX;
                if (srcX >= 0 && srcX < canvasW && srcY >= 0 && srcY < canvasH) {
                    int byteIdx = srcY * rowStride + srcX / 8;
                    int bitIdx = 7 - (srcX % 8);
                    if (canvas1Buf[byteIdx] & (1 << bitIdx)) {
                        p0 = EINK_BLACK; // GFX color 1 = 黑色文字
                    }
                }
            }

            {
                int srcX = screenY;
                int srcY = (canvasH - 1) - (screenX + 1);
                if (srcX >= 0 && srcX < canvasW && srcY >= 0 && srcY < canvasH) {
                    int byteIdx = srcY * rowStride + srcX / 8;
                    int bitIdx = 7 - (srcX % 8);
                    if (canvas1Buf[byteIdx] & (1 << bitIdx)) {
                        p1 = EINK_BLACK;
                    }
                }
            }

            // 4-bit format: high nibble = pixel 0, low nibble = pixel 1
            sendData((p0 << 4) | p1);
        }
    }

    sendCommand(0x12); // DRF
    sendData(0x00);
    delay(1);
    waitUntilIdle();
}

// 竖屏画布尺寸常量
static const int PORTRAIT_W = 480;
static const int PORTRAIT_H = 800;

void DisplayDriver::showText(const char* text) {
    if (!_initialized) return;
    LOG_INFO_F("[Display] 显示文字: %s\n", text);

    GFXcanvas1 canvas(PORTRAIT_W, PORTRAIT_H);
    canvas.fillScreen(0); // 白色背景 (GFX 0 → EINK_WHITE)
    canvas.setTextColor(1); // 黑色文字 (GFX 1 → EINK_BLACK)
    canvas.setTextSize(3);
    canvas.setTextWrap(true);

    // 居中
    int16_t x1, y1;
    uint16_t tw, th;
    canvas.getTextBounds(text, 0, 0, &x1, &y1, &tw, &th);
    int cx = (PORTRAIT_W - tw) / 2;
    int cy = (PORTRAIT_H - th) / 2;
    if (cx < 20) cx = 20;
    if (cy < 20) cy = 20;

    canvas.setCursor(cx, cy);
    canvas.print(text);

    renderCanvasToDisplay(canvas.getBuffer(), PORTRAIT_W, PORTRAIT_H);
    LOG_INFO("[Display] 文字显示完成");
}

void DisplayDriver::showAPGuide(const char* ssid, const char* url) {
    if (!_initialized) return;
    LOG_INFO("[Display] 显示 AP 配网引导");

    GFXcanvas1 canvas(PORTRAIT_W, PORTRAIT_H);
    canvas.fillScreen(0); // 白色背景
    canvas.setTextColor(1); // 黑色文字

    // 标题 - 居中
    canvas.setTextSize(4);
    int16_t x1, y1;
    uint16_t tw, th;
    canvas.getTextBounds("Relive Setup", 0, 0, &x1, &y1, &tw, &th);
    canvas.setCursor((PORTRAIT_W - tw) / 2, 100);
    canvas.print("Relive Setup");

    // 分隔线
    canvas.drawFastHLine(40, 160, PORTRAIT_W - 80, 1);

    // Step 1: WiFi
    canvas.setTextSize(2);
    canvas.setCursor(40, 200);
    canvas.print("1. Connect to WiFi:");

    canvas.setTextSize(3);
    canvas.getTextBounds(ssid, 0, 0, &x1, &y1, &tw, &th);
    canvas.setCursor((PORTRAIT_W - tw) / 2, 250);
    canvas.print(ssid);

    // Step 2: URL
    canvas.setTextSize(2);
    canvas.setCursor(40, 340);
    canvas.print("2. Open in browser:");

    canvas.setTextSize(3);
    canvas.getTextBounds(url, 0, 0, &x1, &y1, &tw, &th);
    canvas.setCursor((PORTRAIT_W - tw) / 2, 390);
    canvas.print(url);

    // Step 3: 配置
    canvas.setTextSize(2);
    canvas.setCursor(40, 480);
    canvas.print("3. Configure WiFi,");
    canvas.setCursor(40, 510);
    canvas.print("   server & schedule");

    // 分隔线
    canvas.drawFastHLine(40, 580, PORTRAIT_W - 80, 1);

    // 底部超时提示
    canvas.setTextSize(2);
    canvas.getTextBounds("Auto-sleep after 3 min", 0, 0, &x1, &y1, &tw, &th);
    canvas.setCursor((PORTRAIT_W - tw) / 2, 620);
    canvas.print("Auto-sleep after 3 min");

    renderCanvasToDisplay(canvas.getBuffer(), PORTRAIT_W, PORTRAIT_H);
    LOG_INFO("[Display] AP 引导显示完成");
}

void DisplayDriver::showSleepMessage(const char* message) {
    if (!_initialized) return;
    LOG_INFO_F("[Display] 显示睡眠提示: %s\n", message);

    GFXcanvas1 canvas(PORTRAIT_W, PORTRAIT_H);
    canvas.fillScreen(0); // 白色背景
    canvas.setTextColor(1); // 黑色文字
    canvas.setTextSize(2);
    canvas.setTextWrap(true);

    // 居中
    int16_t x1, y1;
    uint16_t tw, th;
    canvas.getTextBounds(message, 0, 0, &x1, &y1, &tw, &th);
    int cx = (PORTRAIT_W - tw) / 2;
    int cy = (PORTRAIT_H - th) / 2;
    if (cx < 30) cx = 30;
    if (cy < 30) cy = 30;

    canvas.setCursor(cx, cy);
    canvas.print(message);

    renderCanvasToDisplay(canvas.getBuffer(), PORTRAIT_W, PORTRAIT_H);
    LOG_INFO("[Display] 睡眠提示显示完成");
}
