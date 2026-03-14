#ifndef WEB_PORTAL_H
#define WEB_PORTAL_H

#include <Arduino.h>
#include <WebServer.h>
#include "wifi_manager.h"
#include "nvs_config.h"

class WebPortal {
public:
    void begin(WiFiManager* wifi, NVSConfig* nvs);
    void handleClient();
    bool isConfigured();
    void stop();

private:
    WebServer _server;
    WiFiManager* _wifi;
    NVSConfig* _nvs;
    bool _configured;

    void handleRoot();
    void handleScan();
    void handleSave();
    void handleStatus();
    void handleConfig();
};

#endif // WEB_PORTAL_H
