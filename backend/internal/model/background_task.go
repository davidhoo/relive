package model

import "time"

// BackgroundTaskStatusResponse 是 GET /background/status 的响应，只读、无副作用。
// 反映进程内 BackgroundTaskCoordinator 的当前快照 + advisory 负载采样 + 配置阈值。
// 所有字段向后兼容；新增字段必须可选。unknown 负载值用 -1 表示。
type BackgroundTaskStatusResponse struct {
	// ForegroundActive 表示当前是否有 P0 前台操作进行中（foreground scope 计数 > 0）。
	ForegroundActive bool `json:"foreground_active"`
	// ForegroundCount 是当前持有 foreground scope 的前台操作数。
	ForegroundCount int `json:"foreground_count"`
	// AutoTasksEnabled 表示 P2 自动后台任务是否启用（false 时所有 P2 被 automatic_disabled 拒绝）。
	AutoTasksEnabled bool `json:"auto_tasks_enabled"`
	// Running 是当前正在运行的 automatic 后台任务列表。
	Running []BackgroundTaskRuntimeResponse `json:"running"`
	// Cooldowns 是 class → cooldown 截止时间（RFC3339）的映射，仅未过期的。
	Cooldowns map[string]string `json:"cooldowns"`
	// PendingDedupe 是已 coalesce pending 的 (class, dedupeKey) 列表。
	PendingDedupe []BackgroundTaskDedupeResponse `json:"pending_dedupe"`
	// Load 是最近一次负载采样快照（advisory，-1 表示 unknown）。
	Load BackgroundLoadSnapshotResponse `json:"load"`
	// Thresholds 是 advisory 资源背压阈值（0 表示禁用）。
	Thresholds BackgroundThresholdsResponse `json:"thresholds"`
	// ProtoCacheRebuild 是 protoCache 分批 full rebuild 的进度快照。nil/省略表示当前没有
	// rebuild 在进行（向后兼容：旧客户端忽略该字段即可）。cold_building 标识冷启动构建中。
	ProtoCacheRebuild *ProtoCacheRebuildStatusResponse `json:"proto_cache_rebuild,omitempty"`
	// CapturedAt 是快照采集时间（RFC3339）。
	CapturedAt string `json:"captured_at"`
}

// ProtoCacheRebuildStatusResponse 描述一次 protoCache 分批 full rebuild 的只读进度快照。
// 所有字段向后兼容。state 取值：idle/running/paused/completed/failed；cold_building 为
// 独立布尔，当无可用旧缓存且 rebuild 进行中时为 true，用于前端区分冷启动与普通后台 refresh。
type ProtoCacheRebuildStatusResponse struct {
	Generation   uint64 `json:"generation"`
	State        string `json:"state"`
	ColdBuilding bool   `json:"cold_building"`
	Cursor       int    `json:"cursor"`
	Total        int    `json:"total"`
	Batches      int    `json:"batches"`
	PauseReason  string `json:"pause_reason,omitempty"`
}

// BackgroundTaskRuntimeResponse 描述一个正在运行的后台任务（脱敏，仅 class/dedupe/priority/started_at）。
type BackgroundTaskRuntimeResponse struct {
	Class     string `json:"class"`
	DedupeKey string `json:"dedupe_key,omitempty"`
	Priority  string `json:"priority"`
	StartedAt string `json:"started_at"`
}

// BackgroundTaskDedupeResponse 描述一个 pending 的去重槽位。
type BackgroundTaskDedupeResponse struct {
	Class     string `json:"class"`
	DedupeKey string `json:"dedupe_key"`
}

// BackgroundLoadSnapshotResponse 是负载采样快照。-1 表示 unknown（不支持的平台或解析失败）。
type BackgroundLoadSnapshotResponse struct {
	Load1        float64 `json:"load1"`
	CPUUserPct   float64 `json:"cpu_user_pct"`
	CPUSystemPct float64 `json:"cpu_system_pct"`
	CPUIOWaitPct float64 `json:"cpu_iowait_pct"`
	MemUsedPct   float64 `json:"mem_used_pct"`
}

// BackgroundThresholdsResponse 是 advisory 背压阈值。
type BackgroundThresholdsResponse struct {
	CPUPauseThreshold    float64 `json:"cpu_pause_threshold"`
	IOWaitPauseThreshold float64 `json:"iowait_pause_threshold"`
	MemoryPauseThreshold float64 `json:"memory_pause_threshold"`
	DBLockedCooldownMs   int64   `json:"db_locked_cooldown_ms"`
}

// FormatBackgroundTimeRFC3339 格式化时间为 RFC3339 字符串；零值返回空串。
func FormatBackgroundTimeRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
