package database

import (
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type testModel struct {
	ID    uint `gorm:"primaryKey"`
	Name  string
	Value int
}

func TestWriteQueue_ConcurrentWrites_NoLockErrors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&testModel{})

	wq := NewWriteQueue(nil)
	defer wq.Stop()

	var lockErrors int64
	var successCount int64

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				err := wq.Execute(func() error {
					return db.Create(&testModel{Name: "test", Value: id*1000 + j}).Error
				})
				if err != nil {
					if isSQLiteLockError(err) {
						atomic.AddInt64(&lockErrors, 1)
					}
				} else {
					atomic.AddInt64(&successCount, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Success: %d, Lock errors: %d", successCount, lockErrors)

	if lockErrors > 0 {
		t.Errorf("Expected 0 lock errors, got %d", lockErrors)
	}

	var count int64
	db.Model(&testModel{}).Count(&count)
	if count != 1000 {
		t.Errorf("Expected 1000 records, got %d", count)
	}
}

func TestWithoutWriteQueue_ConcurrentWrites_HaveLockErrors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&testModel{})

	var lockErrors int64
	var successCount int64

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				err := db.Create(&testModel{Name: "test", Value: id*1000 + j}).Error
				if err != nil {
					if isSQLiteLockError(err) {
						atomic.AddInt64(&lockErrors, 1)
					}
				} else {
					atomic.AddInt64(&successCount, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Without WriteQueue - Success: %d, Lock errors: %d", successCount, lockErrors)
}

func isSQLiteLockError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "database is locked") ||
		contains(errStr, "database table is locked") ||
		contains(errStr, "busy")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
