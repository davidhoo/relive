package worker

import (
	"context"
	"sync"
	"testing"

	"github.com/davidhoo/relive/cmd/relive-people-worker/internal/client"
	pkgConfig "github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

var initTestLoggerOnce sync.Once

func initTestLogger(t *testing.T) {
	t.Helper()
	initTestLoggerOnce.Do(func() {
		if err := logger.Init(pkgConfig.LoggingConfig{Level: "error", Console: true}); err != nil {
			t.Fatalf("init test logger: %v", err)
		}
	})
}

func TestPeopleWorkerHandleRuntimeLeaseLostWaitsForDrain(t *testing.T) {
	initTestLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := &PeopleWorker{
		taskManager:          client.NewTaskManager(client.NewAPIClient("http://example.com", "test")),
		runtimeHeartbeatStop: make(chan struct{}),
		stopCh:               make(chan struct{}),
		ctx:                  ctx,
		cancel:               cancel,
	}
	w.inFlightTasks.Store(1)

	w.handleRuntimeLeaseLost(&client.APIError{
		StatusCode: 409,
		Code:       "PEOPLE_RUNTIME_OWNED_BY_OTHER",
		Message:    "analysis runtime owned by other",
	})

	select {
	case <-w.stopCh:
		t.Fatal("worker should not stop before in-flight task drains")
	default:
	}

	w.inFlightTasks.Store(0)
	w.maybeStopAfterDrain()

	select {
	case <-w.stopCh:
	default:
		t.Fatal("worker should stop after draining in-flight tasks")
	}
}

func TestPeopleWorkerHandleRuntimeLeaseLostStopsImmediatelyWhenIdle(t *testing.T) {
	initTestLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := &PeopleWorker{
		taskManager:          client.NewTaskManager(client.NewAPIClient("http://example.com", "test")),
		runtimeHeartbeatStop: make(chan struct{}),
		stopCh:               make(chan struct{}),
		ctx:                  ctx,
		cancel:               cancel,
	}

	w.handleRuntimeLeaseLost(&client.APIError{
		StatusCode: 409,
		Code:       "PEOPLE_RUNTIME_OWNED_BY_OTHER",
		Message:    "analysis runtime owned by other",
	})

	select {
	case <-w.stopCh:
	default:
		t.Fatal("idle worker should stop immediately after lease loss")
	}
}
