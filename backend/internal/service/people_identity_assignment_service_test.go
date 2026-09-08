package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newAssignmentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:assignment_test?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	return db
}

func TestPeopleIdentityAssignment_PreviewAndRevokeVersionConflict(t *testing.T) {
	db := newAssignmentTestDB(t)
	faceRepo := repository.NewFaceRepository(db)
	personRepo := repository.NewPersonRepository(db)
	assignRepo := repository.NewPeopleIdentityAssignmentRepository(db)
	svc := NewPeopleIdentityAssignmentService(assignRepo, faceRepo, personRepo)

	person := &model.Person{Category: model.PersonCategoryFamily}
	require.NoError(t, personRepo.Create(person))
	face := &model.Face{
		PhotoID: 1, PersonID: &person.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.8,
		ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.9,
		AssignmentVersion: 2,
	}
	require.NoError(t, faceRepo.Create(face))

	now := time.Now()
	batch := &model.PeopleIdentityAssignmentBatch{
		OperationID: "op-1", Source: "background", Mode: model.PeopleIdentityModePrimary,
		EngineVersion: identityEngineVersion, StrategyVersion: identityAutoStrategyVersion,
		ConfigFingerprint: "fp", Status: model.PeopleIdentityAssignmentBatchCompleted, StartedAt: now,
	}
	require.NoError(t, assignRepo.CreateBatch(batch))
	pid := person.ID
	require.NoError(t, assignRepo.CreateChanges(nil, []model.PeopleIdentityAssignmentChange{{
		BatchID: batch.ID, ComponentKey: "c-1", FaceID: face.ID,
		DecisionSource: model.PeopleIdentityDecisionSourceProfileAttach,
		OldPersonID: nil, NewPersonID: &pid,
		OldClusterStatus: model.FaceClusterStatusPending, NewClusterStatus: model.FaceClusterStatusAssigned,
		PreviousAssignmentVersion: 1, CommittedAssignmentVersion: 1, // stale vs current 2
		Score: 0.9,
	}}))

	preview, err := svc.PreviewRevoke(batch.ID)
	require.NoError(t, err)
	require.Equal(t, 1, preview.SkippedComponents)
	require.Equal(t, 0, preview.RevertableComponents)
	require.Equal(t, "assignment_version_conflict", preview.Items[0].Reason)

	result, err := svc.Revoke(batch.ID)
	require.NoError(t, err)
	require.Equal(t, 1, result.SkippedComponents)
	require.Equal(t, 0, result.RevertedComponents)

	// 幂等
	again, err := svc.Revoke(batch.ID)
	require.NoError(t, err)
	require.Equal(t, result.RevocationID, again.RevocationID)
}
