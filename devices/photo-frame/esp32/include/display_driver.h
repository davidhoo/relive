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
    // buffer: 7色格式的图像数据
    // 对于 800x480 的 Spectra 6，每个像素用 4bit 表示，每字节2个像素
    // 总大小：800 * 480 / 2 = 192000 bytes
    void display(const uint8_t* buffer, size_t size);

    // 旋转 90 度显示（竖屏图片在横屏上显示）
    // srcBuffer: 480x800 的源图片（4bit格式）
    // 将 480x800 旋转 90 度显示在 800x480 屏幕上
    void displayRotated(const uint8_t* srcBuffer, size_t size);

    // 进入深度睡眠模式
    void sleep();

    // 从睡眠中唤醒
    void wakeup();

    // 检查屏幕是否忙碌
    bool isBusy();

    // 获取屏幕宽度
    int width() { return SCREEN_WIDTH; }

    // 获取屏幕高度
    int height() { return SCREEN_HEIGHT; }

    // 获取每行字节数（4bit格式: 800 / 2 = 400 bytes）
    int bytesPerLine() { return SCREEN_WIDTH / 2; }

    // 获取缓冲区大小（800 * 480 / 2 = 192000 bytes）
    size_t bufferSize() { return (SCREEN_WIDTH * SCREEN_HEIGHT) / 2; }

private:
    bool _initialized;

    // 硬件 SPI 通信
    void spiTransfer(uint8_t data);
    void sendCommand(uint8_t cmd);
    void sendData(uint8_t data);
    void sendData(const uint8_t* data, size_t len);

    // 硬件复位
    void reset();

    // 等待忙碌信号（BUSY引脚变为高电平）
    void waitUntilIdle();
    
    // 初始化序列（快速模式）
    void initFast();
    
    // 初始化序列（标准模式）
    void initNormal();
};

#endif // DISPLAY_DRIVER_H
