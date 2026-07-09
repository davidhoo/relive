package service

import (
	"bufio"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BackgroundLoadSnapshot 是一次运行时负载采样的只读快照。所有数值字段为 -1 时表示
// unknown（不支持的平台或解析失败），调用方须用 isKnown 检查，不得把 unknown 当作真实值。
//
// sampler 只是 advisory：Phase 2/3 的 foreground scope 与 cooldown 规则才是权威机制，
// 负载采样不能单独拒绝 P2（计划硬约束）。CPU 百分比基于两次 /proc/stat 采样的 delta 计算。
type BackgroundLoadSnapshot struct {
	Load1        float64
	CPUUserPct   float64
	CPUSystemPct float64
	CPUIOWaitPct  float64
	MemUsedPct   float64
	CapturedAt   time.Time
}

// unknownLoad 是表示某项指标不可知的哨兵值（< 0）。调用方用 isKnown 判断。
const unknownLoad = -1.0

// IsKnown 报告 v 是否为已知有效值（非 unknown）。负值视为 unknown。
func isKnown(v float64) bool { return v >= 0 }

// backgroundLoadSampler 是轻量运行时负载采样器，Linux-only best effort。
//
// 它只读取 /proc 文件（loadavg、stat、meminfo），不 shell out、不依赖外部命令。
// CPU 百分比需要两次 /proc/stat 采样之间的 delta，故 Sample() 内部缓存上一次的
// cpu 字段累计值。非 Linux 平台所有字段返回 unknown，不阻塞 background work。
//
// 线程安全：通过 mu 保护缓存的 prev CPU 字段。设计为低频调用（调度器每分钟级别），
// 不追求高精度，只供 advisory 背压。
type backgroundLoadSampler struct {
	mu      sync.Mutex
	prevCPU *cpuStatSample // 上一次 /proc/stat cpu 行累计值，nil 表示首次
}

// cpuStatSample 是 /proc/stat 第一行 cpu 的累计 jiffies 采样。
type cpuStatSample struct {
	user, nice, system, idle, iowait, irq, softirq, steal, guest, guestNice uint64
}

func newBackgroundLoadSampler() *backgroundLoadSampler {
	return &backgroundLoadSampler{}
}

// Sample 采集一次负载快照。非 Linux 平台返回全 unknown 快照（不报错）。
// 单次 /proc 读取失败时，对应字段为 unknown，其余字段仍尽量填充。
func (s *backgroundLoadSampler) Sample() BackgroundLoadSnapshot {
	snap := BackgroundLoadSnapshot{
		Load1:        unknownLoad,
		CPUUserPct:   unknownLoad,
		CPUSystemPct: unknownLoad,
		CPUIOWaitPct:  unknownLoad,
		MemUsedPct:   unknownLoad,
		CapturedAt:   time.Now(),
	}
	if runtime.GOOS != "linux" {
		return snap
	}

	if v, ok := parseLoadAvg(); ok {
		snap.Load1 = v
	}
	if user, sys, iowait, cur, ok := s.sampleCPU(); ok {
		snap.CPUUserPct = user
		snap.CPUSystemPct = sys
		snap.CPUIOWaitPct = iowait
		_ = cur
	}
	if mem, ok := parseMemInfo(); ok {
		snap.MemUsedPct = mem
	}
	return snap
}

// sampleCPU 读取 /proc/stat 第一行 cpu，与上次缓存值计算 delta 百分比。
// 返回 (userPct, systemPct, iowaitPct, currentSample, ok)。首次采样无 delta，
// 返回 unknown 但仍缓存当前值供下次使用。
func (s *backgroundLoadSampler) sampleCPU() (float64, float64, float64, *cpuStatSample, bool) {
	cur, ok := readProcStatCPU()
	if !ok {
		return unknownLoad, unknownLoad, unknownLoad, nil, false
	}
	s.mu.Lock()
	prev := s.prevCPU
	s.prevCPU = cur
	s.mu.Unlock()
	if prev == nil {
		// 首次采样无 delta，返回 unknown 但已缓存当前值。
		return unknownLoad, unknownLoad, unknownLoad, cur, false
	}
	totalDelta := cpuTotal(cur) - cpuTotal(prev)
	if totalDelta <= 0 {
		return unknownLoad, unknownLoad, unknownLoad, cur, false
	}
	// user 含 nice；system 含 irq+softirq；iowait 单独。
	userPct := float64(cur.user+cur.nice-prev.user-prev.nice) / float64(totalDelta) * 100
	sysPct := float64(cur.system+cur.irq+cur.softirq-prev.system-prev.irq-prev.softirq) / float64(totalDelta) * 100
	iowaitPct := float64(cur.iowait-prev.iowait) / float64(totalDelta) * 100
	return clampPct(userPct), clampPct(sysPct), clampPct(iowaitPct), cur, true
}

// cpuTotal 返回 cpu 行所有字段的累计和（用于 delta 总量）。
func cpuTotal(c *cpuStatSample) uint64 {
	if c == nil {
		return 0
	}
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal + c.guest + c.guestNice
}

// clampPct 把百分比夹到 [0, 100]，避免计数器回绕或异常输入产生越界值。
func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// parseLoadAvg 解析 /proc/loadavg 第一列（1 分钟负载）。失败返回 (0, false)。
func parseLoadAvg() (float64, bool) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// readProcStatCPU 读取 /proc/stat 第一行 "cpu" 的累计 jiffies。
// 字段顺序：user nice system idle iowait irq softirq steal guest guestNice（部分可能缺失）。
func readProcStatCPU() (*cpuStatSample, bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		return parseProcStatCPU(line), true
	}
	return nil, false
}

// parseProcStatCPU 解析 "cpu  user nice system idle iowait ..." 行。
// 字段不足时缺失部分按 0 处理。
func parseProcStatCPU(line string) *cpuStatSample {
	fields := strings.Fields(line)
	// fields[0] == "cpu"
	c := &cpuStatSample{}
	// 索引映射：1=user 2=nice 3=system 4=idle 5=iowait 6=irq 7=softirq 8=steal 9=guest 10=guestNice
	get := func(i int) uint64 {
		if i >= len(fields) {
			return 0
		}
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return 0
		}
		return v
	}
	c.user = get(1)
	c.nice = get(2)
	c.system = get(3)
	c.idle = get(4)
	c.iowait = get(5)
	c.irq = get(6)
	c.softirq = get(7)
	c.steal = get(8)
	c.guest = get(9)
	c.guestNice = get(10)
	return c
}

// parseMemInfo 解析 /proc/meminfo，返回内存使用百分比（MemTotal/MemAvailable）。
// MemAvailable 缺失时回退 MemFree。失败返回 (0, false)。
func parseMemInfo() (float64, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()
	return parseMemInfoReader(f)
}

// parseMemInfoReader 从任意 reader 解析 meminfo 格式。抽出便于测试喂入字节数据。
func parseMemInfoReader(r io.Reader) (float64, bool) {
	var memTotal, memAvail, memFree uint64
	availSeen := false
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// 值形如 "16384 kB"
		numStr := strings.Fields(val)[0]
		n, err := strconv.ParseUint(numStr, 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			memTotal = n
		case "MemAvailable":
			memAvail = n
			availSeen = true
		case "MemFree":
			memFree = n
		}
	}
	if memTotal == 0 {
		return 0, false
	}
	used := memTotal
	if availSeen {
		used = memTotal - memAvail
	} else {
		used = memTotal - memFree
	}
	if used > memTotal {
		used = memTotal
	}
	return float64(used) / float64(memTotal) * 100, true
}
