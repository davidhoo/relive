#include "web_portal.h"
#include "log.h"
#include <ArduinoJson.h>

// Embedded HTML page (PROGMEM)
static const char PORTAL_HTML[] PROGMEM = R"rawliteral(
<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Relive 相框配置</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#f5f5f5;color:#333;padding:20px;max-width:480px;margin:0 auto}
h1{text-align:center;margin-bottom:24px;font-size:22px;color:#1a1a1a}
.card{background:#fff;border-radius:12px;padding:20px;margin-bottom:16px;box-shadow:0 2px 8px rgba(0,0,0,.08)}
.card h2{font-size:16px;margin-bottom:12px;color:#555}
label{display:block;font-size:14px;margin-bottom:4px;color:#666}
input,select{width:100%;padding:10px 12px;border:1px solid #ddd;border-radius:8px;font-size:15px;margin-bottom:12px;outline:none;transition:border .2s}
input:focus,select:focus{border-color:#4a90d9}
.row{display:flex;gap:12px}
.row>*{flex:1}
.schedules{display:flex;flex-wrap:wrap;gap:8px;margin-bottom:8px}
.schedule-tag{display:inline-flex;align-items:center;background:#e8f0fe;color:#1967d2;padding:6px 12px;border-radius:16px;font-size:14px}
.schedule-tag .remove{margin-left:6px;cursor:pointer;font-weight:bold;color:#999}
.schedule-tag .remove:hover{color:#d93025}
.add-schedule{display:flex;gap:8px;align-items:center}
.add-schedule input{margin-bottom:0;width:80px;text-align:center}
.add-schedule button{padding:8px 16px;border:none;background:#e8f0fe;color:#1967d2;border-radius:8px;cursor:pointer;font-size:14px;white-space:nowrap}
.hint{font-size:12px;color:#999;margin-bottom:12px}
button.primary{width:100%;padding:14px;border:none;background:#1967d2;color:#fff;border-radius:10px;font-size:16px;cursor:pointer;margin-top:8px}
button.primary:hover{background:#1557b0}
button.primary:disabled{background:#ccc;cursor:not-allowed}
.status{text-align:center;padding:20px;color:#666;font-size:14px}
.toast{position:fixed;top:20px;left:50%;transform:translateX(-50%);background:#333;color:#fff;padding:12px 24px;border-radius:8px;font-size:14px;display:none;z-index:100}
</style>
</head>
<body>
<h1>Relive 相框配置</h1>

<div class="card">
<h2>WiFi 设置</h2>
<label for="ssid">WiFi 名称</label>
<select id="ssid-select" onchange="onSSIDSelect()">
<option value="">扫描中...</option>
</select>
<input type="text" id="ssid" placeholder="或手动输入 WiFi 名称" style="display:none">
<label for="pass">WiFi 密码</label>
<input type="password" id="pass" placeholder="输入 WiFi 密码">
</div>

<div class="card">
<h2>服务器设置</h2>
<label for="host">服务器地址</label>
<input type="text" id="host" placeholder="如 192.168.1.100 或 https://example.com">
<div class="row">
<div>
<label for="port">端口</label>
<input type="number" id="port" value="8080" placeholder="8080">
</div>
<div>
<label for="apikey">API Key</label>
<input type="text" id="apikey" placeholder="设备 API Key">
</div>
</div>
</div>

<div class="card">
<h2>刷新计划</h2>
<div class="hint">设置每天刷新照片的时间点（24小时制）。建议每天不超过 5 次刷新以延长待机。</div>
<div class="schedules" id="schedules"></div>
<div class="add-schedule">
<input type="time" id="new-time" value="08:00">
<button onclick="addSchedule()">添加</button>
</div>
</div>

<button class="primary" id="save-btn" onclick="saveConfig()">保存配置</button>
<div class="toast" id="toast"></div>

<script>
var schedules = ['08:00', '20:00'];

function renderSchedules() {
  var el = document.getElementById('schedules');
  el.innerHTML = '';
  schedules.forEach(function(s, i) {
    el.innerHTML += '<span class="schedule-tag">' + s +
      '<span class="remove" onclick="removeSchedule(' + i + ')">x</span></span>';
  });
}

function addSchedule() {
  var t = document.getElementById('new-time').value;
  if (t && schedules.indexOf(t) === -1) {
    schedules.push(t);
    schedules.sort();
    renderSchedules();
  }
}

function removeSchedule(i) {
  schedules.splice(i, 1);
  renderSchedules();
}

function onSSIDSelect() {
  var sel = document.getElementById('ssid-select');
  var inp = document.getElementById('ssid');
  if (sel.value === '__manual__') {
    inp.style.display = 'block';
    inp.focus();
  } else {
    inp.style.display = 'none';
    inp.value = sel.value;
  }
}

function showToast(msg) {
  var t = document.getElementById('toast');
  t.textContent = msg;
  t.style.display = 'block';
  setTimeout(function() { t.style.display = 'none'; }, 3000);
}

function saveConfig() {
  var sel = document.getElementById('ssid-select');
  var ssid = sel.value === '__manual__' ? document.getElementById('ssid').value : sel.value;
  var pass = document.getElementById('pass').value;
  var host = document.getElementById('host').value;
  var port = document.getElementById('port').value || '8080';
  var apikey = document.getElementById('apikey').value;

  if (!ssid) { showToast('请选择或输入 WiFi 名称'); return; }
  if (!host) { showToast('请输入服务器地址'); return; }
  if (!apikey) { showToast('请输入 API Key'); return; }

  var schedStr = schedules.map(function(s) { return s.replace(':', ''); }).join(',');

  var btn = document.getElementById('save-btn');
  btn.disabled = true;
  btn.textContent = '保存中...';

  var xhr = new XMLHttpRequest();
  xhr.open('POST', '/save');
  xhr.setRequestHeader('Content-Type', 'application/json');
  xhr.onload = function() {
    if (xhr.status === 200) {
      btn.textContent = '配置已保存，设备将重启...';
      showToast('配置已保存');
    } else {
      btn.disabled = false;
      btn.textContent = '保存配置';
      showToast('保存失败: ' + xhr.responseText);
    }
  };
  xhr.onerror = function() {
    btn.disabled = false;
    btn.textContent = '保存配置';
    showToast('网络错误');
  };
  xhr.send(JSON.stringify({
    ssid: ssid, pass: pass, host: host,
    port: parseInt(port), apikey: apikey, schedules: schedStr
  }));
}

function loadSSIDs() {
  var xhr = new XMLHttpRequest();
  xhr.open('GET', '/scan');
  xhr.onload = function() {
    if (xhr.status === 200) {
      var data = JSON.parse(xhr.responseText);
      var sel = document.getElementById('ssid-select');
      sel.innerHTML = '<option value="">-- 请选择 --</option>';
      (data.ssids || []).forEach(function(s) {
        sel.innerHTML += '<option value="' + s + '">' + s + '</option>';
      });
      sel.innerHTML += '<option value="__manual__">手动输入...</option>';
      // 如果已有配置的 SSID，自动选中
      if (window._savedSSID) {
        var found = false;
        for (var i = 0; i < sel.options.length; i++) {
          if (sel.options[i].value === window._savedSSID) {
            sel.value = window._savedSSID;
            found = true;
            break;
          }
        }
        if (!found && window._savedSSID) {
          // SSID 不在扫描列表中，切到手动输入
          sel.value = '__manual__';
          var inp = document.getElementById('ssid');
          inp.style.display = 'block';
          inp.value = window._savedSSID;
        }
      }
    }
  };
  xhr.send();
}

function loadConfig() {
  var xhr = new XMLHttpRequest();
  xhr.open('GET', '/config');
  xhr.onload = function() {
    if (xhr.status === 200) {
      var cfg = JSON.parse(xhr.responseText);
      if (cfg.ssid) window._savedSSID = cfg.ssid;
      if (cfg.pass) document.getElementById('pass').value = cfg.pass;
      if (cfg.host) document.getElementById('host').value = cfg.host;
      if (cfg.port) document.getElementById('port').value = cfg.port;
      if (cfg.apikey) document.getElementById('apikey').value = cfg.apikey;
      if (cfg.schedules) {
        // schedules 格式: "0800,2000"，转为 ["08:00","20:00"]
        schedules = cfg.schedules.split(',').filter(function(s){return s;}).map(function(s) {
          return s.substring(0,2) + ':' + s.substring(2);
        });
        renderSchedules();
      }
    }
  };
  xhr.send();
}

renderSchedules();
loadConfig();
loadSSIDs();
</script>
</body>
</html>
)rawliteral";

void WebPortal::begin(WiFiManager* wifi, NVSConfig* nvs) {
    _wifi = wifi;
    _nvs = nvs;
    _configured = false;

    _server.on("/", HTTP_GET, [this]() { handleRoot(); });
    _server.on("/scan", HTTP_GET, [this]() { handleScan(); });
    _server.on("/config", HTTP_GET, [this]() { handleConfig(); });
    _server.on("/save", HTTP_POST, [this]() { handleSave(); });
    _server.on("/status", HTTP_GET, [this]() { handleStatus(); });

    _server.begin(80);
    LOG_INFO("[Portal] Web 配置页面已启动 (端口 80)");
}

void WebPortal::handleClient() {
    _server.handleClient();
}

bool WebPortal::isConfigured() {
    return _configured;
}

void WebPortal::stop() {
    _server.stop();
    LOG_INFO("[Portal] Web 服务已停止");
}

void WebPortal::handleRoot() {
    _server.send_P(200, "text/html", PORTAL_HTML);
}

void WebPortal::handleScan() {
    int count = 0;
    String* ssids = _wifi->getScannedSSIDs(count);

    JsonDocument doc;
    JsonArray arr = doc["ssids"].to<JsonArray>();
    for (int i = 0; i < count; i++) {
        arr.add(ssids[i]);
    }

    String json;
    serializeJson(doc, json);
    _server.send(200, "application/json", json);
}

void WebPortal::handleSave() {
    String body = _server.arg("plain");
    JsonDocument doc;
    DeserializationError error = deserializeJson(doc, body);

    if (error) {
        _server.send(400, "text/plain", "JSON parse error");
        return;
    }

    String ssid = doc["ssid"] | "";
    String pass = doc["pass"] | "";
    String host = doc["host"] | "";
    uint16_t port = doc["port"] | 8080;
    String apikey = doc["apikey"] | "";
    String schedules = doc["schedules"] | "";

    if (ssid.length() == 0 || host.length() == 0) {
        _server.send(400, "text/plain", "Missing required fields");
        return;
    }

    // 保存到 NVS
    _nvs->setWiFi(ssid, pass);
    _nvs->setServer(host, port, apikey);
    _nvs->setSchedules(schedules);
    _nvs->setConfigured(true);
    _nvs->resetAPFailCount();

    _configured = true;

    _server.send(200, "application/json", "{\"success\":true}");
    LOG_INFO("[Portal] 配置已保存");

    // 2 秒后重启
    delay(2000);
    ESP.restart();
}

void WebPortal::handleStatus() {
    JsonDocument doc;
    doc["configured"] = _nvs->isConfigured();
    doc["ap_ssid"] = AP_SSID;

    String json;
    serializeJson(doc, json);
    _server.send(200, "application/json", json);
}

void WebPortal::handleConfig() {
    JsonDocument doc;
    doc["ssid"] = _nvs->getWiFiSSID();
    doc["pass"] = _nvs->getWiFiPass();
    doc["host"] = _nvs->getServerHost();
    doc["port"] = _nvs->getServerPort();
    doc["apikey"] = _nvs->getAPIKey();
    doc["schedules"] = _nvs->getSchedules();

    String json;
    serializeJson(doc, json);
    _server.send(200, "application/json", json);
}
