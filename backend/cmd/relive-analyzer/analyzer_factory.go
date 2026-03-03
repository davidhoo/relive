package main

import (
	analyzer "github.com/davidhoo/relive/cmd/relive-analyzer/internal/analyzer"
	"github.com/davidhoo/relive/cmd/relive-analyzer/internal/config"
)

// createAnalyzer 创建 API 分析器实例
func createAnalyzer(cfg *config.Config) (*analyzer.APIAnalyzer, error) {
	return analyzer.NewAPIAnalyzer(cfg)
}
