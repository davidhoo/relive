#include "nvs_config.h"
#include "log.h"
#include <Preferences.h>

static const char* NVS_NAMESPACE = "relive";

static Preferences prefs;

void NVSConfig::begin() {
    LOG_INFO("[NVS] 初始化配置存储");
    prefs.begin(NVS_NAMESPACE, false);
}

bool NVSConfig::isConfigured() {
    return prefs.getBool("configured", false);
}

void NVSConfig::setConfigured(bool val) {
    prefs.putBool("configured", val);
    LOG_INFO_F("[NVS] configured = %s\n", val ? "true" : "false");
}

String NVSConfig::getWiFiSSID() {
    return prefs.getString("wifi_ssid", "");
}

String NVSConfig::getWiFiPass() {
    return prefs.getString("wifi_pass", "");
}

void NVSConfig::setWiFi(const String& ssid, const String& pass) {
    prefs.putString("wifi_ssid", ssid);
    prefs.putString("wifi_pass", pass);
    LOG_INFO_F("[NVS] WiFi SSID = %s\n", ssid.c_str());
}

String NVSConfig::getServerHost() {
    return prefs.getString("srv_host", "");
}

uint16_t NVSConfig::getServerPort() {
    return prefs.getUShort("srv_port", 8080);
}

String NVSConfig::getAPIKey() {
    return prefs.getString("api_key", "");
}

void NVSConfig::setServer(const String& host, uint16_t port, const String& apiKey) {
    prefs.putString("srv_host", host);
    prefs.putUShort("srv_port", port);
    prefs.putString("api_key", apiKey);
    LOG_INFO_F("[NVS] Server = %s:%d\n", host.c_str(), port);
}

String NVSConfig::getSchedules() {
    return prefs.getString("schedules", "");
}

void NVSConfig::setSchedules(const String& schedules) {
    prefs.putString("schedules", schedules);
    LOG_INFO_F("[NVS] Schedules = %s\n", schedules.c_str());
}

uint8_t NVSConfig::getAPFailCount() {
    return prefs.getUChar("ap_fail_cnt", 0);
}

void NVSConfig::setAPFailCount(uint8_t count) {
    prefs.putUChar("ap_fail_cnt", count);
}

void NVSConfig::resetAPFailCount() {
    prefs.putUChar("ap_fail_cnt", 0);
}

bool NVSConfig::getBootFlag() {
    bool val = prefs.getBool("boot_flag", false);
    LOG_INFO_F("[NVS] 读取 boot_flag = %s\n", val ? "true" : "false");
    return val;
}

void NVSConfig::setBootFlag(bool val) {
    prefs.putBool("boot_flag", val);
    LOG_INFO_F("[NVS] boot_flag = %s\n", val ? "true" : "false");
}
