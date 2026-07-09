package service

import (
	"errors"
	"fmt"
	"testing"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
)

// TestIsSQLiteBusyOrLocked_Sqlite3Error 验证 errors.As 解包到 sqlite3.Error 并按基码判定。
func TestIsSQLiteBusyOrLocked_Sqlite3Error(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"busy", sqlite3.Error{Code: sqlite3.ErrBusy}, true},
		{"locked", sqlite3.Error{Code: sqlite3.ErrLocked}, true},
		// SQLite 扩展码：base | (extended << 8)。busy=5 → 261=5|(1<<8); locked=6 → 262=6|(1<<8).
		{"busy_extended", sqlite3.Error{Code: sqlite3.ErrNo(261)}, true},  // 261 & 0xFF = 5
		{"locked_extended", sqlite3.Error{Code: sqlite3.ErrNo(262)}, true}, // 262 & 0xFF = 6
		{"other_error", sqlite3.Error{Code: sqlite3.ErrNo(1)}, false},      // SQL error
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isSQLiteBusyOrLocked(tc.err))
		})
	}
}

// TestIsSQLiteBusyOrLocked_WrappedError 验证被外层 wrap 的 sqlite3.Error 仍能解包。
func TestIsSQLiteBusyOrLocked_WrappedError(t *testing.T) {
	wrapped := fmt.Errorf("background slice failed: %w", sqlite3.Error{Code: sqlite3.ErrBusy})
	assert.True(t, isSQLiteBusyOrLocked(wrapped))

	wrapped2 := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", sqlite3.Error{Code: sqlite3.ErrLocked}))
	assert.True(t, isSQLiteBusyOrLocked(wrapped2))
}

// TestIsSQLiteBusyOrLocked_BareErrNo 验证裸 ErrNo 通过 errors.Is 判定。
func TestIsSQLiteBusyOrLocked_BareErrNo(t *testing.T) {
	assert.True(t, isSQLiteBusyOrLocked(sqlite3.ErrBusy))
	assert.True(t, isSQLiteBusyOrLocked(sqlite3.ErrLocked))
	assert.False(t, isSQLiteBusyOrLocked(sqlite3.ErrNo(1)))
}

// TestIsSQLiteBusyOrLocked_StringFallback 验证无法解包时的字符串 fallback。
func TestIsSQLiteBusyOrLocked_StringFallback(t *testing.T) {
	assert.True(t, isSQLiteBusyOrLocked(errors.New("database is locked")))
	assert.True(t, isSQLiteBusyOrLocked(errors.New("SQL logic: database table is locked by tx")))
	assert.True(t, isSQLiteBusyOrLocked(errors.New("database is busy")))
	// 非锁错误不误判。
	assert.False(t, isSQLiteBusyOrLocked(errors.New("no such table: faces")))
	assert.False(t, isSQLiteBusyOrLocked(errors.New("disk I/O error")))
}
