#ifndef DISPLAY_DRIVER_H
#define DISPLAY_DRIVER_H

#include <Arduino.h>
#include <SPI.h>
#include "config.h"

// E Ink Spectra 6 颜色定义 (7色)
// 4bit 格式：每个像素用4bit表示，每字节包含2个像素
enum EInkColor {
    EINK_BLACK   = 0x00,  // 0000
    EINK_WHITE   = 0x01,  // 0001
    EINK_YELLOW  = 0x02,  // 0010
    EINK_RED     = 0x03,  // 0011
    EINK_BLUE    = 0x05,  // 0101
    EINK_GREEN   = 0x06,  // 0110
    EINK_CLEAN   = 0x07   // 0111
};

// 8bit 格式（每字节2个像素）
#define COLOR_BLACK   0x00
#define COLOR_WHITE   0x11
#define COLOR_YELLOW  0x22
#define COLOR_RED     0x33
#define COLOR_BLUE    0x55
#define COLOR_GREEN   0x66
#define COLOR_CLEAN   0x77

// 显示驱动类
class DisplayDriver {
public:
    DisplayDriver();

    // 初始化屏幕
    bool begin();

    // 清屏（白色）
    void clear();

    // 全屏刷新显示缓冲区内容
    void display(const uint8_t* buffer, size_t size);

    // 旋转 90 度显示
    void displayRotated(const uint8_t* srcBuffer, size_t size);

    // 进入深度睡眠模式
    void sleep();

    // 从睡眠中唤醒
    void wakeup();

    // 检查屏幕是否忙碌
    bool isBusy();

    // 全屏居中显示文字（黑底白字或白底黑字）
    void showText(const char* text);

    // AP 配网引导屏
    void showAPGuide(const char* ssid, const char* url);

    // 退避睡眠提示
    void showSleepMessage(const char* message);

    int width() { return SCREEN_WIDTH; }
    int height() { return SCREEN_HEIGHT; }
    int bytesPerLine() { return SCREEN_WIDTH / 2; }
    size_t bufferSize() { return (SCREEN_WIDTH * SCREEN_HEIGHT) / 2; }

private:
    bool _initialized;

    void spiTransfer(uint8_t data);
    void sendCommand(uint8_t cmd);
    void sendData(uint8_t data);
    void sendData(const uint8_t* data, size_t len);
    void reset();
    void waitUntilIdle(unsigned long timeoutMs = 30000);
    void initFast();
    void initNormal();

    // 文字渲染辅助：将 GFXcanvas1 的 1-bit 像素写入 4-bit framebuffer 并刷新
    void renderCanvasToDisplay(uint8_t* canvas1Buffer, int canvasW, int canvasH);
};

#endif // DISPLAY_DRIVER_H
