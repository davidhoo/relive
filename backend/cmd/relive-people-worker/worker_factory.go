package main

import (
	"github.com/davidhoo/relive/cmd/relive-people-worker/internal/config"
	"github.com/davidhoo/relive/cmd/relive-people-worker/internal/worker"
)

// createWorker 创建 People Worker 实例
func createWorker(cfg *config.Config) (*worker.PeopleWorker, error) {
	return worker.NewPeopleWorker(cfg)
}
