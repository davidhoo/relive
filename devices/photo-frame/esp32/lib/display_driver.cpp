/**
 * @file display_driver.cpp
 * @brief 墨水屏驱动抽象层实现
 */

#include "display_driver.h"
#include "config.h"

// GDEP073E01 命令定义
#define CMD_PANEL_SETTING       0x00
#define CMD_POWER_SETTING       0x01
#define CMD_POWER_OFF           0x02
#define CMD_POWER_OFF_SEQUENCE  0x03
#define CMD_POWER_ON            0x04
#define CMD_POWER_ON_MEASURE    0x05
#define CMD_BOOSTER_SOFT_START  0x06
#define CMD_DEEP_SLEEP          0x07
#define CMD_DISPLAY_START_TRANS 0x10
#define CMD_DISPLAY_REFRESH     0x12
#define CMD_IMG_PROCESS         0x13
#define CMD_LUT_SETTING         0x20
#define CMD_MULTI_INI           0x21
#define CMD_MULTI_INI_COLOR     0x22
#define CMD_PLL_CONTROL         0x30
#define CMD_TEMP_SENSOR         0x40
#define CMD_TEMP_CALIB          0x41
#define CMD_TEMP_WRITE          0x42
#define CMD_TEMP_READ           0x43
#define CMD_VCOM_GLITCH         0x50
#define CMD_RESOLUTION          0x61
#define CMD_STATUS              0x71
#define CMD_VCOM_VALUE          0x81

DisplayDriver::DisplayDriver()
    : _type(DISPLAY_CUSTOM)
    , _pinCS(EPD_PIN_CS)
    , _pinDC(EPD_PIN_DC)
    , _pinRST(EPD_PIN_RST)
    , _pinBUSY(EPD_PIN_BUSY)
    , _width(480)
    , _height(800)
    , _colors(4)
{
}

DisplayDriver::~DisplayDriver()
{
}

bool DisplayDriver::init(DisplayType type, uint8_t cs, uint8_t dc, uint8_t rst, uint8_t busy)
{
    _type = type;
    _pinCS = cs;
    _pinDC = dc;
    _pinRST = rst;
    _pinBUSY = busy;

    // 根据类型设置分辨率
    switch (type) {
    case DISPLAY_GDEP073E01:
        _width = 480;
        _height = 800;
        _colors = 4;
        break;
    case DISPLAY_GDEY075T7:
        _width = 800;
        _height = 480;
        _colors = 2;
        break;
    case DISPLAY_GDEW075C64:
        _width = 640;
        _height = 384;
        _colors = 4;
        break;
    default:
        break;
    }

    // 初始化引脚
    pinMode(_pinCS, OUTPUT);
    pinMode(_pinDC, OUTPUT);
    pinMode(_pinRST, OUTPUT);
    pinMode(_pinBUSY, INPUT);

    digitalWrite(_pinCS, HIGH);
    digitalWrite(_pinDC, HIGH);
    digitalWrite(_pinRST, HIGH);

    // 初始化 SPI
    SPI.begin(EPD_PIN_SCK, -1, EPD_PIN_MOSI, _pinCS);

#if LOG_LEVEL >= 3
    Serial.print("[Display] Initializing type ");
    Serial.print(type);
    Serial.print(" (");
    Serial.print(_width);
    Serial.print("x");
    Serial.print(_height);
    Serial.println(")");
#endif

    // 硬件复位
    hardwareReset();

    // 等待屏幕就绪
    delay(100);

    // 初始化序列（简化版，实际需要根据具体屏幕调整）
    // 这里只是示例框架，具体实现需要根据屏幕数据手册编写

#if LOG_LEVEL >= 3
    Serial.println("[Display] Initialized");
#endif

    return true;
}

void DisplayDriver::hardwareReset()
{
    digitalWrite(_pinRST, LOW);
    delay(10);
    digitalWrite(_pinRST, HIGH);
    delay(10);
    waitBusy();
}

void DisplayDriver::waitBusy()
{
    while (digitalRead(_pinBUSY) == HIGH) {
        delay(10);
    }
}

bool DisplayDriver::isBusy() const
{
    return digitalRead(_pinBUSY) == HIGH;
}

bool DisplayDriver::waitUntilIdle(uint32_t timeoutMs)
{
    uint32_t start = millis();
    while (isBusy()) {
        if (millis() - start > timeoutMs) {
            return false;
        }
        delay(10);
    }
    return true;
}

void DisplayDriver::spiTransfer(uint8_t data)
{
    SPI.transfer(data);
}

void DisplayDriver::writeCommand(uint8_t cmd)
{
    digitalWrite(_pinDC, LOW);
    digitalWrite(_pinCS, LOW);
    spiTransfer(cmd);
    digitalWrite(_pinCS, HIGH);
}

void DisplayDriver::writeData(uint8_t data)
{
    digitalWrite(_pinDC, HIGH);
    digitalWrite(_pinCS, LOW);
    spiTransfer(data);
    digitalWrite(_pinCS, HIGH);
}

void DisplayDriver::writeData(const uint8_t* data, size_t len)
{
    digitalWrite(_pinDC, HIGH);
    digitalWrite(_pinCS, LOW);
    for (size_t i = 0; i < len; i++) {
        spiTransfer(data[i]);
    }
    digitalWrite(_pinCS, HIGH);
}

void DisplayDriver::clear(uint8_t color)
{
#if LOG_LEVEL >= 3
    Serial.println("[Display] Clearing...");
#endif

    // 发送清屏命令
    writeCommand(CMD_RESOLUTION);
    writeData(_width >> 8);
    writeData(_width & 0xFF);
    writeData(_height >> 8);
    writeData(_height & 0xFF);

    // 填充颜色（需要根据具体屏幕实现）
    size_t pixelCount = _width * _height;
    uint8_t fillValue = (color == EPD_WHITE) ? 0xFF : 0x00;

    writeCommand(CMD_DISPLAY_START_TRANS);
    for (size_t i = 0; i < pixelCount / 4; i++) {
        writeData(fillValue);
    }

    // 刷新显示
    writeCommand(CMD_DISPLAY_REFRESH);
    waitBusy();

#if LOG_LEVEL >= 3
    Serial.println("[Display] Clear done");
#endif
}

bool DisplayDriver::displayBin(const BinFileData& binData)
{
    if (!binData.data || binData.size == 0) {
        return false;
    }

    // 检查分辨率是否匹配
    if (binData.width != _width || binData.height != _height) {
        if (binData.width != _height || binData.height != _width) {
            // 尝试旋转90度匹配
#if LOG_LEVEL >= 2
            Serial.println("[Display] Warning: Resolution mismatch");
#endif
        }
    }

    // 计算数据起始位置（跳过 bin header）
    uint8_t ditherModeLen = binData.data[6];
    size_t dataOffset = 12 + ditherModeLen;
    const uint8_t* pixelData = binData.data + dataOffset;
    size_t pixelDataSize = binData.size - dataOffset;

#if LOG_LEVEL >= 3
    Serial.print("[Display] Displaying bin: ");
    Serial.print(binData.width);
    Serial.print("x");
    Serial.print(binData.height);
    Serial.print(", ");
    Serial.print(pixelDataSize);
    Serial.println(" bytes");
#endif

    // 设置分辨率
    writeCommand(CMD_RESOLUTION);
    writeData(binData.width >> 8);
    writeData(binData.width & 0xFF);
    writeData(binData.height >> 8);
    writeData(binData.height & 0xFF);

    // 发送图像数据
    if (!sendIndexedData(pixelData, binData.width, binData.height, binData.paletteColors)) {
        return false;
    }

    // 刷新显示
    writeCommand(CMD_DISPLAY_REFRESH);

#if LOG_LEVEL >= 3
    Serial.println("[Display] Refresh started");
#endif

    return true;
}

bool DisplayDriver::sendIndexedData(const uint8_t* indexedData, uint16_t width, uint16_t height, uint8_t colors)
{
    // 基类提供默认实现：将调色板索引转换为字节流
    // 子类可以重写此方法以适应特定屏幕的数据格式

    size_t pixelCount = width * height;

    // 对于4色屏幕，每4个像素占1字节
    // 对于2色屏幕，每8个像素占1字节

    writeCommand(CMD_DISPLAY_START_TRANS);

    if (colors == 4) {
        // 4色：每个像素2bit，每字节4个像素
        // 需要打包数据
        for (size_t i = 0; i < pixelCount; i += 4) {
            uint8_t byte = 0;
            for (int j = 0; j < 4 && (i + j) < pixelCount; j++) {
                uint8_t pixel = indexedData[i + j] & 0x03;
                byte |= (pixel << (6 - j * 2));
            }
            writeData(byte);
        }
    } else {
        // 2色：每个像素1bit，每字节8个像素
        for (size_t i = 0; i < pixelCount; i += 8) {
            uint8_t byte = 0;
            for (int j = 0; j < 8 && (i + j) < pixelCount; j++) {
                if (indexedData[i + j] == EPD_BLACK) {
                    byte |= (1 << (7 - j));
                }
            }
            writeData(byte);
        }
    }

    return true;
}

void DisplayDriver::sleep()
{
#if LOG_LEVEL >= 3
    Serial.println("[Display] Entering sleep mode");
#endif

    writeCommand(CMD_POWER_OFF);
    waitBusy();

    writeCommand(CMD_DEEP_SLEEP);
    writeData(0xA5);  // 检查码
}

void DisplayDriver::wakeup()
{
#if LOG_LEVEL >= 3
    Serial.println("[Display] Waking up");
#endif

    hardwareReset();
}
