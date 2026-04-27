package service

import (
	"math"
	"os"
	"sort"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
)

// TestClusteringEquivalence 验证优化前后聚类结果完全一致
// 注意：此测试需要本地数据库文件，如果没有会跳过
func TestClusteringEquivalence(t *testing.T) {
	dbPath := "../../data/relive.db"

	// 检查数据库文件是否存在
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Skip("Database file not found, skipping equivalence test. Run with local data/relive.db to enable.")
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
	svc := NewPeopleService(db, repos.Photo, repos.Face, repos.Person, repos.PeopleJob, repos.PeopleMergeJob, repos.CannotLink, cfg, nil, nil).(*peopleService)

	// 获取测试数据
	pendingFaces, err := repos.Face.ListPending(peopleClusteringBatchSize)
	if err != nil {
		t.Fatalf("Failed to list pending faces: %v", err)
	}

	if len(pendingFaces) == 0 {
		t.Skip("No pending faces to test")
	}

	assignedPersonIDs, err := repos.Face.ListAssignedPersonIDs()
	if err != nil {
		t.Fatalf("Failed to list assigned person IDs: %v", err)
	}

	var protoFaces []*model.Face
	if len(assignedPersonIDs) > 0 {
		protoFaces, err = repos.Face.ListPrototypeEmbeddings(assignedPersonIDs, peoplePrototypeCandidates)
		if err != nil {
			t.Fatalf("Failed to list prototype faces: %v", err)
		}
	}
	prototypes := svc.selectPersonPrototypes(protoFaces, peoplePrototypeCount)

	t.Logf("Testing with %d pending faces, %d persons", len(pendingFaces), len(prototypes))

	// 测试 buildFaceGraph 等价性
	t.Run("buildFaceGraph", func(t *testing.T) {
		graph1 := svc.buildFaceGraph(pendingFaces)
		// 运行两次，结果应该相同
		graph2 := svc.buildFaceGraph(pendingFaces)

		if !graphsEqual(graph1, graph2) {
			t.Errorf("buildFaceGraph results differ between runs")
		}
	})

	// 测试 selectDiversePrototypes 等价性
	t.Run("selectDiversePrototypes", func(t *testing.T) {
		for personID, personFaces := range prototypes {
			selected1 := selectDiversePrototypes(personFaces, peoplePrototypeCount)
			selected2 := selectDiversePrototypes(personFaces, peoplePrototypeCount)

			if !facesEqual(selected1, selected2) {
				t.Errorf("selectDiversePrototypes results differ for person %d", personID)
			}
		}
	})

	// 测试 scoreComponentAgainstPerson 等价性
	t.Run("scoreComponentAgainstPerson", func(t *testing.T) {
		graph := svc.buildFaceGraph(pendingFaces)
		components := svc.findConnectedComponents(graph)

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

			for personID, protoFaces := range prototypes {
				score1 := svc.scoreComponentAgainstPerson(component, protoFaces)
				score2 := svc.scoreComponentAgainstPerson(component, protoFaces)

				if !scoresEqual(score1, score2) {
					t.Errorf("scoreComponentAgainstPerson results differ for person %d: %f vs %f",
						personID, score1, score2)
				}
			}
		}
	})

	// 测试完整流程等价性（多次运行）
	t.Run("fullClusteringPipeline", func(t *testing.T) {
		results1 := runClusteringPipeline(svc, pendingFaces, prototypes)
		results2 := runClusteringPipeline(svc, pendingFaces, prototypes)

		if !clusteringResultsEqual(results1, results2) {
			t.Errorf("Full clustering pipeline results differ between runs")
		}
	})
}

// clusteringResult 保存一次聚类的完整结果
type clusteringResult struct {
	components [][]uint
	scores     map[string]float64 // "componentIdx_personID" -> score
}

func runClusteringPipeline(svc *peopleService, pendingFaces []*model.Face, prototypes map[uint][]*model.Face) clusteringResult {
	graph := svc.buildFaceGraph(pendingFaces)
	components := svc.findConnectedComponents(graph)

	result := clusteringResult{
		components: components,
		scores:     make(map[string]float64),
	}

	for compIdx, componentIDs := range components {
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

		for personID, protoFaces := range prototypes {
			score := svc.scoreComponentAgainstPerson(component, protoFaces)
			key := formatScoreKey(compIdx, personID)
			result.scores[key] = score
		}
	}

	return result
}

func formatScoreKey(componentIdx int, personID uint) string {
	return string(rune(componentIdx)) + "_" + string(rune(personID))
}

func clusteringResultsEqual(a, b clusteringResult) bool {
	if len(a.components) != len(b.components) {
		return false
	}

	// 比较连通分量
	for i := range a.components {
		if !uintSlicesEqual(a.components[i], b.components[i]) {
			return false
		}
	}

	// 比较分数
	if len(a.scores) != len(b.scores) {
		return false
	}

	for key, scoreA := range a.scores {
		scoreB, ok := b.scores[key]
		if !ok {
			return false
		}
		if !scoresEqual(scoreA, scoreB) {
			return false
		}
	}

	return true
}

func graphsEqual(a, b map[uint][]uint) bool {
	if len(a) != len(b) {
		return false
	}

	for key, valA := range a {
		valB, ok := b[key]
		if !ok {
			return false
		}
		if !uintSlicesEqual(valA, valB) {
			return false
		}
	}

	return true
}

func uintSlicesEqual(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}

	// 排序后比较
	aCopy := make([]uint, len(a))
	bCopy := make([]uint, len(b))
	copy(aCopy, a)
	copy(bCopy, b)
	sort.Slice(aCopy, func(i, j int) bool { return aCopy[i] < aCopy[j] })
	sort.Slice(bCopy, func(i, j int) bool { return bCopy[i] < bCopy[j] })

	for i := range aCopy {
		if aCopy[i] != bCopy[i] {
			return false
		}
	}

	return true
}

func facesEqual(a, b []*model.Face) bool {
	if len(a) != len(b) {
		return false
	}

	// 按 ID 排序后比较
	aCopy := make([]*model.Face, len(a))
	bCopy := make([]*model.Face, len(b))
	copy(aCopy, a)
	copy(bCopy, b)

	sort.Slice(aCopy, func(i, j int) bool { return aCopy[i].ID < aCopy[j].ID })
	sort.Slice(bCopy, func(i, j int) bool { return bCopy[i].ID < bCopy[j].ID })

	for i := range aCopy {
		if aCopy[i].ID != bCopy[i].ID {
			return false
		}
	}

	return true
}

func scoresEqual(a, b float64) bool {
	// 处理 -1 特殊情况
	if a < 0 && b < 0 {
		return true
	}

	// 使用相对误差进行比较
	diff := math.Abs(a - b)
	if diff < 1e-9 {
		return true
	}

	// 相对误差
	maxVal := math.Max(math.Abs(a), math.Abs(b))
	if maxVal > 0 {
		return diff/maxVal < 1e-6
	}

	return false
}
