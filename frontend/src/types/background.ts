export interface BackgroundTaskRuntime {
  class: string
  dedupe_key?: string
  priority: string
  started_at: string
}

export interface BackgroundTaskDedupe {
  class: string
  dedupe_key: string
}

export interface BackgroundLoadSnapshot {
  load1: number
  cpu_user_pct: number
  cpu_system_pct: number
  cpu_iowait_pct: number
  mem_used_pct: number
}

export interface BackgroundThresholds {
  cpu_pause_threshold: number
  iowait_pause_threshold: number
  memory_pause_threshold: number
  db_locked_cooldown_ms: number
}

export interface BackgroundTaskStatus {
  foreground_active: boolean
  foreground_count: number
  auto_tasks_enabled: boolean
  running: BackgroundTaskRuntime[]
  cooldowns: Record<string, string>
  pending_dedupe: BackgroundTaskDedupe[]
  load: BackgroundLoadSnapshot
  thresholds: BackgroundThresholds
  captured_at: string
}
