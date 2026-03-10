/**
 * @file display_driver.h
 * @brief 墨水屏驱动抽象层
 *
 * 支持多种墨水屏驱动，通过抽象层统一接口
 */

#ifndef DISPLAY_DRIVER_H
#define DISPLAY_DRIVER_H

#include <Arduino.h>
#include <SPI.h>
#include "api_client.h"

// 显示驱动类型
enum DisplayType {
    DISPLAY_GDEP073E01,     // 7.3寸 800x480 彩色
    DISPLAY_GDEY075T7,      // 7.5寸 800x480 黑白
    DISPLAY_GDEW075C64,     // 7.5寸 640x384 黑白红黄
    DISPLAY_CUSTOM          // 自定义
};

// 调色板颜色 (4色墨水屏: BWRY)
enum EpdColor {
    EPD_BLACK   = 0,
    EPD_WHITE   = 1,
    EPD_RED     = 2,    // 或黄色，取决于屏幕
    EPD_YELLOW  = 3
};

class DisplayDriver {
public:
    DisplayDriver();
    virtual ~DisplayDriver();

    /**
     * @brief 初始化显示驱动
     * @param type 显示类型
     * @param cs CS引脚
     * @param dc DC引脚
     * @param rst RST引脚
     * @param busy BUSY引脚
     * @return 是否成功
     */
    virtual bool init(DisplayType type, uint8_t cs, uint8_t dc, uint8_t rst, uint8_t busy);

    /**
     * @brief 清屏
     * @param color 填充颜色
     */
    virtual void clear(uint8_t color = EPD_WHITE);

    /**
     * @brief 显示 Bin 文件数据
     * @param binData bin文件数据
     * @return 是否成功
     *
     * Bin 文件格式:
     * - 数据是调色板索引（每个像素1字节）
     * - 需要映射到实际屏幕颜色
     */
    virtual bool displayBin(const BinFileData& binData);

    /**
     * @brief 进入睡眠模式
     */
    virtual void sleep();

    /**
     * @brief 唤醒
     */
    virtual void wakeup();

    /**
     * @brief 获取屏幕宽度
     */
    uint16_t getWidth() const { return _width; }

    /**
     * @brief 获取屏幕高度
     */
    uint16_t getHeight() const { return _height; }

    /**
     * @brief 是否正在刷新
     */
    bool isBusy() const;

    /**
     * @brief 等待刷新完成
     * @param timeoutMs 超时时间
     */
    bool waitUntilIdle(uint32_t timeoutMs = 30000);

protected:
    DisplayType _type;
    uint8_t _pinCS;
    uint8_t _pinDC;
    uint8_t _pinRST;
    uint8_t _pinBUSY;
    uint16_t _width;
    uint16_t _height;
    uint8_t _colors;

    // SPI 通信
    void spiTransfer(uint8_t data);
    void writeCommand(uint8_t cmd);
    void writeData(uint8_t data);
    void writeData(const uint8_t* data, size_t len);

    // 硬件复位
    void hardwareReset();

    // 等待忙信号
    void waitBusy();

    // 发送调色板索引数据到屏幕
    // 子类需要实现具体的发送逻辑
    virtual bool sendIndexedData(const uint8_t* indexedData, uint16_t width, uint16_t height, uint8_t colors);
};

#endif // DISPLAY_DRIVER_H
