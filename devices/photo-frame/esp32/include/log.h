#ifndef LOG_H
#define LOG_H

#include "config.h"

// Unified log macros
// LOG_LEVEL: 0=OFF, 1=ERROR, 2=INFO, 3=DEBUG

#if LOG_LEVEL >= 1
#define LOG_ERROR(msg) DEBUG_SERIAL.println(msg)
#define LOG_ERROR_F(msg, ...) DEBUG_SERIAL.printf(msg, __VA_ARGS__)
#else
#define LOG_ERROR(msg)
#define LOG_ERROR_F(msg, ...)
#endif

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

#endif // LOG_H
