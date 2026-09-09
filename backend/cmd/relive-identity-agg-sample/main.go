// Package main 在数据库副本上打印组件单向 vs 人物双向聚合的代表样本（只读）。
// 不写库；用于工作二论证债，不输出 embedding。
package main

import (
	"fmt"
	"os"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	dbPath := "/tmp/relive-diag-slim2.db"
	if len(os.Args) > 1 {
		dbPath = os.Args[1]
	}
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=ro&_query_only=true", dbPath)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		panic(err)
	}
	pr := repository.NewPersonIdentityProfileRepository(db)
	modelSig := "http://relive-ml:5050"
	engine, n, err := service.BuildMergeSuggestionDiagnoseEngine(
		pr,
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		modelSig,
		service.IdentityProfileMatcherConfig{
			EmbeddingModel:  modelSig,
			RescueThreshold: 0.65,
			Margin:          0.05,
			MinCenterFaces:  3,
		},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("ann_centers=%d db=%s\n", n, dbPath)

	pairs := []service.PersonPair{
		{TargetID: 265250, CandidateID: 265274},
		{TargetID: 265250, CandidateID: 285426},
		{TargetID: 265250, CandidateID: 382656},
		{TargetID: 267444, CandidateID: 272927},
		{TargetID: 265274, CandidateID: 271594},
	}
	got := engine.ComparePeople(pairs)
	for _, p := range pairs {
		r := got[p]
		if r.Best == nil {
			fmt.Printf("pair %d-%d status=%s block=%s\n", p.TargetID, p.CandidateID, r.Status, r.BlockReason)
			continue
		}
		b := r.Best
		fmt.Printf("pair %d-%d status=%s score=%.4f support=%d\n",
			p.TargetID, p.CandidateID, r.Status, b.Score, b.MinSupportCount)
	}

	centers, err := pr.ListActiveCentersByPersonIDs([]uint{265250, 265274, 271594}, modelSig)
	if err != nil {
		panic(err)
	}
	mk := func(pid uint, cs []*model.PersonIdentityCenter, kind string) service.IdentityEvidence {
		units := make([]service.IdentityEvidenceUnit, 0, len(cs))
		for _, c := range cs {
			emb := model.DecodeEmbedding(c.CentroidEmbedding)
			units = append(units, service.IdentityEvidenceUnit{
				CenterID: c.ID, PersonID: pid, Vector: emb, Weight: 1,
				SupportCount: c.SupportCount, SimilarityP10: c.SimilarityP10,
			})
		}
		return service.IdentityEvidence{Kind: kind, PersonID: pid, Units: units}
	}
	big := centers[265250]
	peer := centers[265274]
	huge := centers[271594]
	if len(big) == 0 || len(peer) == 0 || len(huge) == 0 {
		fmt.Println("missing centers for component sample")
		os.Exit(1)
	}
	if len(huge) > 30 {
		huge = huge[:30]
	}
	comp := mk(0, big[:1], "component")
	full := mk(265250, big, "person")
	peerEv := mk(265274, peer, "person")
	hugeEv := mk(271594, huge, "person")

	cSelf := service.ScoreComponentAgainstPerson(comp, full)
	cPeer := service.ScoreComponentAgainstPerson(comp, peerEv)
	cHuge := service.ScoreComponentAgainstPerson(comp, hugeEv)
	bPeer := service.ScoreIdentityEvidence(full, peerEv)
	bHuge := service.ScoreIdentityEvidence(full, hugeEv)

	fmt.Printf("comp(1/%d of 265250)->self score=%.4f single=%v support=%d\n",
		len(big), cSelf.Score, cSelf.SingleDirection, cSelf.MinSupportCount)
	fmt.Printf("comp(1/%d of 265250)->265274 score=%.4f single=%v support=%d\n",
		len(big), cPeer.Score, cPeer.SingleDirection, cPeer.MinSupportCount)
	fmt.Printf("comp(1/%d of 265250)->huge271594(n=%d) score=%.4f single=%v\n",
		len(big), len(huge), cHuge.Score, cHuge.SingleDirection)
	fmt.Printf("bidir 265250(n=%d)<->265274 score=%.4f fwd=%.4f rev=%.4f\n",
		len(big), bPeer.Score, bPeer.ForwardScore, bPeer.ReverseScore)
	fmt.Printf("bidir 265250<->huge271594(n=%d) score=%.4f fwd=%.4f rev=%.4f\n",
		len(huge), bHuge.Score, bHuge.ForwardScore, bHuge.ReverseScore)
}
