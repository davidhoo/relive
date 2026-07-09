package service

import (
	"errors"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// isSQLiteBusyOrLocked 报告 err 是否为 SQLite busy/locked 错误（P2 后台任务遇此应
// 进入 cooldown 而非 spin）。检测顺序：
//  1. errors.As 解包到 sqlite3.Error，比较 Code 为 ErrBusy(5)/ErrLocked(6) 或其
//     扩展码（SQLITE_BUSY_EXTENDED 262/SQLITE_LOCKED_EXTENDED 266 等，按基码 5/6 判定）。
//  2. errors.Is 与 sqlite3.ErrBusy / sqlite3.ErrLocked 比较（ErrNo 是可比值类型）。
//  3. 字符串 fallback："database is locked" / "database table is locked" / "database is busy"，
//     用于 GORM 包装或无法解包的场景（最后手段，避免误判）。
//
// 注意：本 helper 仅用于 P2 automatic 后台路径决定是否进入 cooldown。前台操作绝不因
// 此 sleep——前台可上报 telemetry，但不能改变用户可见错误。
func isSQLiteBusyOrLocked(err error) bool {
	if err == nil {
		return false
	}

	// 1. errors.As 到 sqlite3.Error，按基码判定。
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		base := int(sqliteErr.Code) & 0xFF // 扩展码低 8 位是基码
		if base == int(sqlite3.ErrBusy) || base == int(sqlite3.ErrLocked) {
			return true
		}
	}

	// 2. errors.Is 与已知 ErrNo 常量比较（覆盖裸 ErrNo 错误）。
	if errors.Is(err, sqlite3.ErrBusy) || errors.Is(err, sqlite3.ErrLocked) {
		return true
	}

	// 3. 字符串 fallback（最后手段）。
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "database is busy") {
		return true
	}
	return false
}
