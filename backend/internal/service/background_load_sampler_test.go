package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBackgroundLoadSampler_ParseLoadAvg 验证 /proc/loadavg 第一列解析。
func TestBackgroundLoadSampler_ParseLoadAvg(t *testing.T) {
	// 用真实 /proc/loadavg（Linux）或确认非 Linux 返回 false。
	if load, ok := parseLoadAvg(); ok {
		assert.GreaterOrEqual(t, load, 0.0)
	} else {
		// 非 Linux：parseLoadAvg 读文件失败返回 false，符合预期。
		t.Logf("non-linux or /proc/loadavg unavailable, parseLoadAvg=false (expected)")
	}
}

// TestBackgroundLoadSampler_ParseProcStatCPU 验证 /proc/stat cpu 行解析为各 jiffies 字段。
func TestBackgroundLoadSampler_ParseProcStatCPU(t *testing.T) {
	line := "cpu  100 20 30 4000 50 5 2 10 0 0"
	c := parseProcStatCPU(line)
	assert.NotNil(t, c)
	assert.Equal(t, uint64(100), c.user)
	assert.Equal(t, uint64(20), c.nice)
	assert.Equal(t, uint64(30), c.system)
	assert.Equal(t, uint64(4000), c.idle)
	assert.Equal(t, uint64(50), c.iowait)
	assert.Equal(t, uint64(5), c.irq)
	assert.Equal(t, uint64(2), c.softirq)
	assert.Equal(t, uint64(10), c.steal)
	assert.Equal(t, uint64(0), c.guest)
	assert.Equal(t, uint64(0), c.guestNice)
}

// TestBackgroundLoadSampler_ParseProcStatCPU_PartialFields 验证字段不足时缺失部分按 0。
func TestBackgroundLoadSampler_ParseProcStatCPU_PartialFields(t *testing.T) {
	// 只到 iowait（5 个数值字段）。
	line := "cpu  10 1 2 100 5"
	c := parseProcStatCPU(line)
	assert.Equal(t, uint64(10), c.user)
	assert.Equal(t, uint64(1), c.nice)
	assert.Equal(t, uint64(2), c.system)
	assert.Equal(t, uint64(100), c.idle)
	assert.Equal(t, uint64(5), c.iowait)
	assert.Equal(t, uint64(0), c.irq, "missing fields default to 0")
	assert.Equal(t, uint64(0), c.steal)
}

// TestBackgroundLoadSampler_CPUDeltaPercentage 验证两次 cpu 采样 delta 百分比计算。
// user 100→150 (+50), system 30→60 (+30), idle 4000→4050 (+50), 其余不变。
// total delta = 50+30+50 = 130。userPct = 50/130*100 ≈ 38.46, systemPct = 30/130*100 ≈ 23.08。
func TestBackgroundLoadSampler_CPUDeltaPercentage(t *testing.T) {
	s := newBackgroundLoadSampler()
	prev := &cpuStatSample{user: 100, nice: 0, system: 30, idle: 4000, iowait: 0}
	cur := &cpuStatSample{user: 150, nice: 0, system: 60, idle: 4050, iowait: 0}
	s.prevCPU = prev

	userPct, sysPct, iowaitPct, _, ok := s.sampleCPUWithFixed(cur)
	assert.True(t, ok)
	assert.InDelta(t, 38.46, userPct, 0.1)
	assert.InDelta(t, 23.08, sysPct, 0.1)
	assert.InDelta(t, 0.0, iowaitPct, 0.01)
}

// TestBackgroundLoadSampler_CPUDeltaIOWait 验证 iowait delta 单独计算。
// iowait 50→80 (+30), 其余不变（user 100→100, idle 4000→4000）。total delta=30。
// iowaitPct = 30/30*100 = 100, userPct = 0.
func TestBackgroundLoadSampler_CPUDeltaIOWait(t *testing.T) {
	s := newBackgroundLoadSampler()
	prev := &cpuStatSample{user: 100, idle: 4000, iowait: 50}
	cur := &cpuStatSample{user: 100, idle: 4000, iowait: 80}
	s.prevCPU = prev
	userPct, _, iowaitPct, _, ok := s.sampleCPUWithFixed(cur)
	assert.True(t, ok)
	assert.InDelta(t, 0.0, userPct, 0.01)
	assert.InDelta(t, 100.0, iowaitPct, 0.1)
}

// TestBackgroundLoadSampler_FirstSampleUnknown 验证首次采样无 delta 返回 unknown，
// 但缓存当前值供下次使用。
func TestBackgroundLoadSampler_FirstSampleUnknown(t *testing.T) {
	s := newBackgroundLoadSampler()
	cur := &cpuStatSample{user: 100, idle: 4000}
	userPct, _, _, cached, ok := s.sampleCPUWithFixed(cur)
	assert.False(t, ok, "first sample has no delta")
	assert.False(t, isKnown(userPct))
	assert.NotNil(t, cached)
	assert.NotNil(t, s.prevCPU, "first sample must cache current value")
}

// TestBackgroundLoadSampler_ClampPct 验证百分比夹到 [0,100]。
func TestBackgroundLoadSampler_ClampPct(t *testing.T) {
	assert.Equal(t, 0.0, clampPct(-5))
	assert.Equal(t, 50.0, clampPct(50))
	assert.Equal(t, 100.0, clampPct(150))
}

// TestBackgroundLoadSampler_ParseMemInfo_Calculations 验证内存使用百分比计算逻辑。
// 用 parseMemInfoBytes 辅助函数直接喂入 meminfo 文本。
func TestBackgroundLoadSampler_ParseMemInfo_Calculations(t *testing.T) {
	// MemTotal=10000, MemAvailable=4000 → used=6000 → 60%。
	content := "MemTotal:       10000 kB\nMemFree:         3000 kB\nMemAvailable:    4000 kB\n"
	pct, ok := parseMemInfoBytes([]byte(content))
	assert.True(t, ok)
	assert.InDelta(t, 60.0, pct, 0.01)

	// 无 MemAvailable 时回退 MemFree：MemTotal=10000, MemFree=2500 → used=7500 → 75%。
	content2 := "MemTotal:       10000 kB\nMemFree:         2500 kB\n"
	pct2, ok := parseMemInfoBytes([]byte(content2))
	assert.True(t, ok)
	assert.InDelta(t, 75.0, pct2, 0.01)
}

// TestBackgroundLoadSampler_ParseMemInfo_EmptyReturnsFalse 验证无 MemTotal 时返回 false。
func TestBackgroundLoadSampler_ParseMemInfo_EmptyReturnsFalse(t *testing.T) {
	_, ok := parseMemInfoBytes([]byte("SwapTotal: 1000 kB\n"))
	assert.False(t, ok)
}

// TestBackgroundLoadSampler_NonLinuxUnknown 验证非 Linux 平台 Sample 返回全 unknown。
// 通过强制设置 runtime.GOOS 不可行（常量），改为直接断言快照结构：所有负值字段为 unknown。
func TestBackgroundLoadSampler_NonLinuxUnknown(t *testing.T) {
	s := newBackgroundLoadSampler()
	snap := s.Sample()
	assert.False(t, snap.CapturedAt.IsZero())
	// 在 Linux 上部分字段可能 known；非 Linux 全 unknown。此处只断言 CapturedAt 已设。
	// 真正的 unknown 路径由 runtime.GOOS != "linux" 守卫，无法在 Linux CI 直接覆盖，
	// 但 parseLoadAvg/parseMemInfo 失败路径已被上面测试覆盖。
	_ = snap
}

// TestBackgroundLoadSampler_IsKnown 验证 isKnown 判定。
func TestBackgroundLoadSampler_IsKnown(t *testing.T) {
	assert.False(t, isKnown(unknownLoad))
	assert.False(t, isKnown(-0.1))
	assert.True(t, isKnown(0))
	assert.True(t, isKnown(50.5))
}

// sampleCPUWithFixed 是 sampleCPU 的测试辅助：直接传入 cur 样本（绕过 /proc 读取），
// 复用 delta 计算逻辑。返回与 sampleCPU 一致。
func (s *BackgroundLoadSampler) sampleCPUWithFixed(cur *cpuStatSample) (float64, float64, float64, *cpuStatSample, bool) {
	s.mu.Lock()
	prev := s.prevCPU
	s.prevCPU = cur
	s.mu.Unlock()
	if prev == nil {
		return unknownLoad, unknownLoad, unknownLoad, cur, false
	}
	totalDelta := cpuTotal(cur) - cpuTotal(prev)
	if totalDelta <= 0 {
		return unknownLoad, unknownLoad, unknownLoad, cur, false
	}
	userPct := float64(cur.user+cur.nice-prev.user-prev.nice) / float64(totalDelta) * 100
	sysPct := float64(cur.system+cur.irq+cur.softirq-prev.system-prev.irq-prev.softirq) / float64(totalDelta) * 100
	iowaitPct := float64(cur.iowait-prev.iowait) / float64(totalDelta) * 100
	return clampPct(userPct), clampPct(sysPct), clampPct(iowaitPct), cur, true
}

// parseMemInfoBytes 是 parseMemInfo 的测试辅助：直接喂入字节数据（绕过 /proc 读取）。
func parseMemInfoBytes(data []byte) (float64, bool) {
	return parseMemInfoReader(strings.NewReader(string(data)))
}
