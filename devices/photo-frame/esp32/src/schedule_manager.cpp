#include "schedule_manager.h"
#include "log.h"
#include <time.h>
#include <sys/time.h>

// 从 __DATE__ 提取编译年份（格式 "Mar 12 2026"）
static int getCompileYear() {
    const char* date = __DATE__;  // "Mmm DD YYYY"
    return atoi(date + 7);       // 偏移 7 取年份
}

static int getCompileMonth() {
    const char* months = "JanFebMarAprMayJunJulAugSepOctNovDec";
    const char* date = __DATE__;
    char mon[4] = { date[0], date[1], date[2], 0 };
    const char* p = strstr(months, mon);
    if (!p) return 1;
    return (int)(p - months) / 3 + 1;
}

void ScheduleManager::parseSchedules(const String& scheduleStr) {
    _schedules.clear();

    if (scheduleStr.length() == 0) {
        LOG_INFO("[Schedule] 无刷新计划");
        return;
    }

    String str = scheduleStr;
    while (str.length() > 0) {
        int commaIdx = str.indexOf(',');
        String token;
        if (commaIdx >= 0) {
            token = str.substring(0, commaIdx);
            str = str.substring(commaIdx + 1);
        } else {
            token = str;
            str = "";
        }

        token.trim();
        if (token.length() == 4) {
            // HHMM 格式
            SchedulePoint sp;
            sp.hour = token.substring(0, 2).toInt();
            sp.minute = token.substring(2, 4).toInt();
            if (sp.hour < 24 && sp.minute < 60) {
                _schedules.push_back(sp);
                LOG_DEBUG_F("[Schedule] 添加计划: %02d:%02d\n", sp.hour, sp.minute);
            }
        } else if (token.length() == 5 && token.charAt(2) == ':') {
            // HH:MM 格式
            SchedulePoint sp;
            sp.hour = token.substring(0, 2).toInt();
            sp.minute = token.substring(3, 5).toInt();
            if (sp.hour < 24 && sp.minute < 60) {
                _schedules.push_back(sp);
                LOG_DEBUG_F("[Schedule] 添加计划: %02d:%02d\n", sp.hour, sp.minute);
            }
        }
    }

    LOG_INFO_F("[Schedule] 共 %d 个计划点\n", _schedules.size());
}

bool ScheduleManager::hasValidSchedules() {
    return _schedules.size() > 0;
}

bool ScheduleManager::isTimeValid() {
    struct tm timeinfo;
    if (!getLocalTime(&timeinfo, 0)) {
        return false;
    }
    // 与编译时间比对：年月必须 >= 编译时的年月
    int year = timeinfo.tm_year + 1900;
    int month = timeinfo.tm_mon + 1;
    int compYear = getCompileYear();
    int compMonth = getCompileMonth();
    return (year > compYear) || (year == compYear && month >= compMonth);
}

void ScheduleManager::syncTimeFromServer(long serverTimestamp) {
    if (serverTimestamp <= 0) return;

    struct timeval tv;
    tv.tv_sec = serverTimestamp;
    tv.tv_usec = 0;
    settimeofday(&tv, nullptr);

    struct tm timeinfo;
    getLocalTime(&timeinfo, 0);
    LOG_INFO_F("[Schedule] 服务器对时: %04d-%02d-%02d %02d:%02d:%02d\n",
               timeinfo.tm_year + 1900, timeinfo.tm_mon + 1, timeinfo.tm_mday,
               timeinfo.tm_hour, timeinfo.tm_min, timeinfo.tm_sec);
}

bool ScheduleManager::syncNTP() {
    LOG_INFO("[Schedule] NTP 同步...");
    configTime(GMT_OFFSET_SEC, DST_OFFSET_SEC, NTP_SERVER);

    struct tm timeinfo;
    // 等待最多 10 秒获取 NTP 时间
    if (!getLocalTime(&timeinfo, 10000)) {
        LOG_ERROR("[Schedule] NTP 同步失败");
        return false;
    }

    LOG_INFO_F("[Schedule] NTP 同步成功: %04d-%02d-%02d %02d:%02d:%02d\n",
               timeinfo.tm_year + 1900, timeinfo.tm_mon + 1, timeinfo.tm_mday,
               timeinfo.tm_hour, timeinfo.tm_min, timeinfo.tm_sec);
    return true;
}

time_t ScheduleManager::getNextScheduleEpoch(struct tm* now) {
    time_t nowEpoch = mktime(now);
    time_t bestEpoch = 0;
    time_t bestDiff = LONG_MAX;

    for (auto& sp : _schedules) {
        // 今天的这个时间点
        struct tm target = *now;
        target.tm_hour = sp.hour;
        target.tm_min = sp.minute;
        target.tm_sec = 0;
        time_t targetEpoch = mktime(&target);

        time_t diff = targetEpoch - nowEpoch;
        if (diff < MIN_SLEEP_SEC) {
            // 太近或已过，试明天
            targetEpoch += 86400;
            diff = targetEpoch - nowEpoch;
        }

        if (diff < bestDiff) {
            bestDiff = diff;
            bestEpoch = targetEpoch;
        }
    }

    return bestEpoch;
}

uint64_t ScheduleManager::calculateSleepDurationMs() {
#ifdef DEBUG_MODE
    LOG_INFO_F("[Schedule] DEBUG_MODE: 固定间隔 %d ms\n", REFRESH_INTERVAL_MS);
    return (uint64_t)REFRESH_INTERVAL_MS;
#endif

    if (!isTimeValid()) {
        LOG_INFO_F("[Schedule] 时间无效，使用固定间隔 %d ms\n", REFRESH_INTERVAL_MS);
        return (uint64_t)REFRESH_INTERVAL_MS;
    }

    if (!hasValidSchedules()) {
        LOG_INFO_F("[Schedule] 无计划，使用固定间隔 %d ms\n", REFRESH_INTERVAL_MS);
        return (uint64_t)REFRESH_INTERVAL_MS;
    }

    struct tm timeinfo;
    getLocalTime(&timeinfo, 0);

    time_t nextEpoch = getNextScheduleEpoch(&timeinfo);
    time_t nowEpoch = mktime(&timeinfo);
    long sleepSec = nextEpoch - nowEpoch;

    if (sleepSec < MIN_SLEEP_SEC) {
        sleepSec = MIN_SLEEP_SEC;
    }

    LOG_INFO_F("[Schedule] 当前: %02d:%02d, 下次唤醒在 %ld 秒后\n",
               timeinfo.tm_hour, timeinfo.tm_min, sleepSec);

    return (uint64_t)sleepSec * 1000ULL;
}
