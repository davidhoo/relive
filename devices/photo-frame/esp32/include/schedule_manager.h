#ifndef SCHEDULE_MANAGER_H
#define SCHEDULE_MANAGER_H

#include <Arduino.h>
#include <vector>
#include "config.h"

class ScheduleManager {
public:
    // 解析刷新计划字符串，如 "0630,1200,1800"
    void parseSchedules(const String& scheduleStr);

    // 计算到下一个计划点的睡眠时长（毫秒）
    // 考虑 DEBUG_MODE、时间有效性等
    uint64_t calculateSleepDurationMs();

    // 是否有有效的计划
    bool hasValidSchedules();

    // 检查 RTC 时间是否有效（> 编译时间）
    bool isTimeValid();

    // 通过 X-Server-Time 校准 RTC
    void syncTimeFromServer(long serverTimestamp);

    // NTP 同步（仅在 AP 配网保存时调用）
    bool syncNTP();

private:
    struct SchedulePoint {
        uint8_t hour;
        uint8_t minute;
    };
    std::vector<SchedulePoint> _schedules;

    // 获取下一个计划点的 epoch 时间
    time_t getNextScheduleEpoch(struct tm* now);
};

#endif // SCHEDULE_MANAGER_H
