#ifndef DISPLAY_DRIVER_H
#define DISPLAY_DRIVER_H

#include <Arduino.h>
#include <SPI.h>
#include "config.h"

// E Ink Spectra 6 颜色定义 (6色)
enum EInkColor {
    EINK_BLACK   = 0x00,
    EINK_WHITE   = 0x01,
    EINK_GREEN   = 0x02,
    EINK_BLUE    = 0x03,
    EINK_RED     = 0x04,
    EINK_YELLOW  = 0x05,
    EINK_ORANGE  = 0x06  // 某些面板支持
};

// 显示驱动类
class DisplayDriver {
public:
    DisplayDriver();

    // 初始化屏幕
    bool begin();

    // 清屏（白色）
    void clear();

    // 全屏刷新显示缓冲区内容
    // buffer: 6色压缩格式的图像数据
    // 对于 800x480 的 Spectra 6，每个像素用 3bit 表示
    void display(const uint8_t* buffer);

    // 旋转 90 度显示（竖屏图片在横屏上显示）
    // srcBuffer: 480x800 的源图片
    // 将 480x800 旋转 90 度显示在 800x480 屏幕上
    void displayRotated(const uint8_t* srcBuffer);

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

    // 获取每行字节数（用于 Spectra 6: 800 * 3bit / 8 = 300 bytes）
    int bytesPerLine() { return (SCREEN_WIDTH * 3 + 7) / 8; }

    // 获取缓冲区大小
    size_t bufferSize() { return bytesPerLine() * SCREEN_HEIGHT; }

private:
    bool _initialized;

    // 硬件 SPI 通信
    void spiTransfer(uint8_t data);
    void sendCommand(uint8_t cmd);
    void sendData(uint8_t data);
    void sendData(const uint8_t* data, size_t len);

    // 复位屏幕
    void reset();

    // 等待忙碌信号
    void waitUntilIdle();
};

#endif // DISPLAY_DRIVER_H
