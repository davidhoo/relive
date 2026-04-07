package service

import (
	"fmt"
	"os"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
)

// BenchmarkPeopleClustering 测试人物聚类性能
// 注意：此测试需要本地数据库文件，如果没有会跳过
func BenchmarkPeopleClustering(b *testing.B) {
	dbPath := "../../data/relive.db"

	// 检查数据库文件是否存在
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		b.Skip("Database file not found, skipping benchmark. Run with local data/relive.db to enable.")
	}

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Type: "sqlite",
			Path: dbPath,
		},
	}

	db, err := database.Init(cfg.Database)
	if err != nil {
		b.Fatalf("Failed to init database: %v", err)
	}

	repos := repository.NewRepositories(db)
	svc := NewPeopleService(db, repos.Photo, repos.Face, repos.Person, repos.PeopleJob, repos.CannotLink, cfg, nil, nil).(*peopleService)

	// 预热：获取 pending faces
	pendingFaces, err := repos.Face.ListPending(peopleClusteringBatchSize)
	if err != nil {
		b.Fatalf("Failed to list pending faces: %v", err)
	}

	assignedPersonIDs, err := repos.Face.ListAssignedPersonIDs()
	if err != nil {
		b.Fatalf("Failed to list assigned person IDs: %v", err)
	}

	var protoFaces []*model.Face
	if len(assignedPersonIDs) > 0 {
		protoFaces, err = repos.Face.ListTopByPersonIDs(assignedPersonIDs, peoplePrototypeCandidates)
		if err != nil {
			b.Fatalf("Failed to list prototype faces: %v", err)
		}
	}
	prototypes := svc.selectPersonPrototypes(protoFaces, peoplePrototypeCount)

	b.Logf("Pending faces: %d, Prototypes: %d persons", len(pendingFaces), len(prototypes))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 模拟完整的聚类流程（使用优化后的预解码路径）
		graph := svc.buildFaceGraph(pendingFaces, svc.linkThreshold())
		components := svc.findConnectedComponents(graph)

		// 预解码所有 prototype embeddings 一次（生产优化）
		prototypesWithEmb := make(map[uint][]faceWithEmbedding, len(prototypes))
		for personID, protoList := range prototypes {
			prototypesWithEmb[personID] = decodeFacesWithEmbeddings(protoList)
		}

		for _, componentIDs := range components {
			component := make([]*model.Face, 0, len(componentIDs))
			for _, faceID := range componentIDs {
				for _, f := range pendingFaces {
					if f.ID == faceID {
						component = append(component, f)
						break
					}
				}
			}
			if len(component) == 0 {
				continue
			}

			// 使用优化路径：预解码 component embeddings
			componentWithEmb := decodeFacesWithEmbeddings(component)
			if len(componentWithEmb) == 0 {
				continue
			}

			blockedPersons := make(map[uint]bool)
			svc.attachComponentToExistingPersonWithEmbeddings(
				componentWithEmb, prototypesWithEmb, blockedPersons, prototypes, svc.attachThreshold(),
			)
		}
	}
}

// TestPeopleClusteringProfile 生成 CPU profile 文件
// 注意：此测试默认跳过，需要设置 ENABLE_PROFILE_TEST=1 环境变量才能运行
func TestPeopleClusteringProfile(t *testing.T) {
	// 检查环境变量，默认跳过
	if os.Getenv("ENABLE_PROFILE_TEST") != "1" {
		t.Skip("Skipping profile test. Set ENABLE_PROFILE_TEST=1 to enable.")
	}

	dbPath := "../../data/relive.db"

	// 检查数据库文件是否存在
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skip("Database file not found, skipping profile test. Run with local data/relive.db to enable.")
	}

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Type: "sqlite",
			Path: dbPath,
		},
	}

	db, err := database.Init(cfg.Database)
	if err != nil {
		t.Fatalf("Failed to init database: %v", err)
	}

	repos := repository.NewRepositories(db)
	svc := NewPeopleService(db, repos.Photo, repos.Face, repos.Person, repos.PeopleJob, repos.CannotLink, cfg, nil, nil).(*peopleService)

	// 获取数据
	pendingFaces, err := repos.Face.ListPending(peopleClusteringBatchSize)
	if err != nil {
		t.Fatalf("Failed to list pending faces: %v", err)
	}

	assignedPersonIDs, err := repos.Face.ListAssignedPersonIDs()
	if err != nil {
		t.Fatalf("Failed to list assigned person IDs: %v", err)
	}

	var protoFaces []*model.Face
	if len(assignedPersonIDs) > 0 {
		protoFaces, err = repos.Face.ListTopByPersonIDs(assignedPersonIDs, peoplePrototypeCandidates)
		if err != nil {
			t.Fatalf("Failed to list prototype faces: %v", err)
		}
	}
	prototypes := svc.selectPersonPrototypes(protoFaces, peoplePrototypeCount)

	t.Logf("Pending faces: %d, Assigned persons: %d, Prototypes: %d persons",
		len(pendingFaces), len(assignedPersonIDs), len(prototypes))

	// 创建 profile 文件
	f, err := os.Create("/tmp/people_clustering.prof")
	if err != nil {
		t.Fatalf("Failed to create profile file: %v", err)
	}
	defer f.Close()

	// 开始 CPU profiling
	if err := pprof.StartCPUProfile(f); err != nil {
		t.Fatalf("Failed to start CPU profile: %v", err)
	}
	defer pprof.StopCPUProfile()

	// 执行多次聚类以累积足够的样本
	// 模拟生产环境 runIncrementalClustering 的优化路径
	start := time.Now()
	iterations := 100
	for i := 0; i < iterations; i++ {
		graph := svc.buildFaceGraph(pendingFaces, svc.linkThreshold())
		components := svc.findConnectedComponents(graph)

		// 预解码所有 prototype embeddings 一次（生产优化）
		prototypesWithEmb := make(map[uint][]faceWithEmbedding, len(prototypes))
		for personID, protoList := range prototypes {
			prototypesWithEmb[personID] = decodeFacesWithEmbeddings(protoList)
		}

		for _, componentIDs := range components {
			component := make([]*model.Face, 0, len(componentIDs))
			for _, faceID := range componentIDs {
				for _, f := range pendingFaces {
					if f.ID == faceID {
						component = append(component, f)
						break
					}
				}
			}
			if len(component) == 0 {
				continue
			}

			// 使用优化路径：预解码 component embeddings
			componentWithEmb := decodeFacesWithEmbeddings(component)
			if len(componentWithEmb) == 0 {
				continue
			}

			blockedPersons := make(map[uint]bool)
			svc.attachComponentToExistingPersonWithEmbeddings(
				componentWithEmb, prototypesWithEmb, blockedPersons, prototypes, svc.attachThreshold(),
			)
		}
	}
	duration := time.Since(start)

	t.Logf("Completed %d iterations in %v (avg: %v per iteration)",
		iterations, duration, duration/time.Duration(iterations))

	fmt.Printf("Profile saved to /tmp/people_clustering.prof\n")
	fmt.Printf("Analyze with: go tool pprof /tmp/people_clustering.prof\n")
}
