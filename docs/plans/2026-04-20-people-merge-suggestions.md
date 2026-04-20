# People Merge Suggestions Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a persistent, low-priority background task that generates merge suggestions for `family / friend` people, lets users exclude or apply candidates with review UI, and remembers human exclusions via `cannot-link`.

**Architecture:** Add two new persistence models for suggestions and suggestion items, reuse current person prototype / embedding scoring to compute candidate similarity, persist long-running task state in `app_config`, and expose a dedicated review flow through People APIs and the People page. The background worker should process small batches continuously, mark itself dirty on people mutations, and avoid competing aggressively with the existing people detection/clustering pipeline.

**Tech Stack:** Go, Gin, GORM, SQLite, Vue 3, TypeScript, Element Plus

---

### Task 1: Add merge-suggestion schema, DTOs, and config knobs

**Files:**
- Create: `backend/internal/model/person_merge_suggestion.go`
- Modify: `backend/internal/model/dto.go`
- Modify: `backend/pkg/config/config.go`
- Modify: `backend/pkg/database/database.go`
- Modify: `backend/pkg/database/database_test.go`
- Modify: `backend/config.dev.yaml`
- Modify: `backend/config.prod.yaml`

**Step 1: Write the failing test**

Add migration coverage in `backend/pkg/database/database_test.go` asserting:

```go
func TestAutoMigrateAddsPersonMergeSuggestionTables(t *testing.T) {
    db := openMigratedTestDB(t)

    for _, table := range []string{
        "person_merge_suggestions",
        "person_merge_suggestion_items",
    } {
        if !db.Migrator().HasTable(table) {
            t.Fatalf("expected %s table to exist after migration", table)
        }
    }
}

func TestAutoMigrateAddsPersonMergeSuggestionConstraints(t *testing.T) {
    db := openMigratedTestDB(t)

    if err := db.Exec(
        "INSERT INTO person_merge_suggestions (target_person_id, target_category_snapshot, status, candidate_count, top_similarity) VALUES (?, ?, ?, ?, ?)",
        1, "family", "pending", 2, 0.62,
    ).Error; err != nil {
        t.Fatalf("expected pending suggestion insert to succeed: %v", err)
    }
}
```

Extend the same test file to reject invalid statuses and assert the new config field exists on `config.PeopleConfig`.

**Step 2: Run test to verify it fails**

Run:

```bash
cd backend && go test -count=1 ./pkg/database -run 'TestAutoMigrateAddsPersonMergeSuggestion'
```

Expected: FAIL because the new tables, enums, and config fields do not exist yet.

**Step 3: Write minimal implementation**

Implement:

- `backend/internal/model/person_merge_suggestion.go`

```go
type PersonMergeSuggestion struct {
    ID                     uint
    TargetPersonID         uint
    TargetCategorySnapshot string
    Status                 string
    CandidateCount         int
    TopSimilarity          float64
    ReviewedAt             *time.Time
}

type PersonMergeSuggestionItem struct {
    ID               uint
    SuggestionID     uint
    CandidatePersonID uint
    SimilarityScore  float64
    Rank             int
    Status           string
}
```

- Add task / stats / list / review DTOs to `backend/internal/model/dto.go`
- Add `MergeSuggestionThreshold` and low-speed tuning knobs to `backend/pkg/config/config.go`
- Register the new models in `backend/pkg/database/database.go`
- Add default example values to `backend/config.dev.yaml` and `backend/config.prod.yaml`

**Step 4: Run test to verify it passes**

Run:

```bash
cd backend && go test -count=1 ./pkg/database -run 'TestAutoMigrateAddsPersonMergeSuggestion'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add backend/internal/model/person_merge_suggestion.go backend/internal/model/dto.go backend/pkg/config/config.go backend/pkg/database/database.go backend/pkg/database/database_test.go backend/config.dev.yaml backend/config.prod.yaml
git commit -m "feat: add merge suggestion schema and config"
```

### Task 2: Add merge-suggestion repository and persistence rules

**Files:**
- Create: `backend/internal/repository/person_merge_suggestion_repo.go`
- Create: `backend/internal/repository/person_merge_suggestion_repo_test.go`
- Modify: `backend/internal/repository/repository.go`

**Step 1: Write the failing test**

Add repository tests covering:

```go
func TestPersonMergeSuggestionRepository_ReplacePendingForTarget(t *testing.T) {}
func TestPersonMergeSuggestionRepository_ListPendingWithItems(t *testing.T) {}
func TestPersonMergeSuggestionRepository_MarkItemsExcluded(t *testing.T) {}
func TestPersonMergeSuggestionRepository_MarkItemsMerged(t *testing.T) {}
func TestPersonMergeSuggestionRepository_CandidateCanOnlyBelongToOnePendingSuggestion(t *testing.T) {}
```

The tests should assert:

- replacing a target's pending suggestion obsoletes the old one and inserts a fresh one
- items are returned ordered by `rank`
- exclusion / merge updates item statuses and suggestion terminal state correctly
- the same candidate cannot remain pending under two suggestions at once

**Step 2: Run test to verify it fails**

Run:

```bash
cd backend && go test -count=1 ./internal/repository -run 'TestPersonMergeSuggestionRepository_'
```

Expected: FAIL because the repository does not exist yet.

**Step 3: Write minimal implementation**

Implement `backend/internal/repository/person_merge_suggestion_repo.go` with methods such as:

```go
type PersonMergeSuggestionRepository interface {
    ReplacePendingForTarget(targetPersonID uint, targetCategory string, items []model.PersonMergeSuggestionItem) error
    ListPending(page, pageSize int) ([]*model.PersonMergeSuggestion, int64, error)
    GetByID(id uint) (*model.PersonMergeSuggestion, error)
    GetItems(suggestionID uint, status string) ([]*model.PersonMergeSuggestionItem, error)
    MarkItemsStatus(suggestionID uint, candidateIDs []uint, status string) error
    UpdateSuggestionStatus(id uint, status string, reviewedAt *time.Time) error
    FindPendingSuggestionByCandidate(candidatePersonID uint) (*model.PersonMergeSuggestion, error)
}
```

Use transactions to keep suggestion and item updates atomic, and register the repository in `backend/internal/repository/repository.go`.

**Step 4: Run test to verify it passes**

Run:

```bash
cd backend && go test -count=1 ./internal/repository -run 'TestPersonMergeSuggestionRepository_'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add backend/internal/repository/person_merge_suggestion_repo.go backend/internal/repository/person_merge_suggestion_repo_test.go backend/internal/repository/repository.go
git commit -m "feat: add merge suggestion repository"
```

### Task 3: Implement the background slice service and dirty-state persistence

**Files:**
- Create: `backend/internal/service/person_merge_suggestion_service.go`
- Create: `backend/internal/service/person_merge_suggestion_service_test.go`
- Modify: `backend/internal/service/service.go`
- Modify: `backend/internal/service/scheduler.go`
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/photo_scan_service.go`

**Step 1: Write the failing test**

Add service tests covering:

```go
func TestPersonMergeSuggestionService_BuildsPendingSuggestionsForFamilyAndFriendTargets(t *testing.T) {}
func TestPersonMergeSuggestionService_SkipsCannotLinkCandidates(t *testing.T) {}
func TestPersonMergeSuggestionService_AssignsCandidateToBestTargetOnly(t *testing.T) {}
func TestPersonMergeSuggestionService_PauseResumeAndRebuildPersistState(t *testing.T) {}
func TestPersonMergeSuggestionService_SlowsDownWhenPeopleBacklogIsHigh(t *testing.T) {}
```

Use real `Person`, `Face`, and `CannotLinkConstraint` rows with embeddings so the tests exercise the same prototype-selection and cosine-scoring path used by `people_service.go`.

**Step 2: Run test to verify it fails**

Run:

```bash
cd backend && go test -count=1 ./internal/service -run 'TestPersonMergeSuggestionService_'
```

Expected: FAIL because the service and scheduler integration do not exist yet.

**Step 3: Write minimal implementation**

Implement `backend/internal/service/person_merge_suggestion_service.go` with:

```go
type PersonMergeSuggestionService interface {
    GetTask() *model.PersonMergeSuggestionTask
    GetStats() (*model.PersonMergeSuggestionStatsResponse, error)
    GetBackgroundLogs() []string
    Pause() error
    Resume() error
    Rebuild() error
    MarkDirty(reason string) error
    RunBackgroundSlice() error
    ExcludeCandidates(suggestionID uint, candidateIDs []uint) error
    ApplySuggestion(suggestionID uint, candidateIDs []uint) error
    ListPending(page, pageSize int) ([]model.PersonMergeSuggestionResponse, int64, error)
    GetPendingByID(id uint) (*model.PersonMergeSuggestionResponse, error)
}
```

Implementation requirements:

- persist long-running state in `app_config`, e.g. `people.merge_suggestions.state`
- process only a small batch of `family / friend` targets per slice
- reuse `selectPersonPrototypes`, `decodeFacesWithEmbeddings`, and cosine scoring from `people_service.go`
- use the new repository to replace pending suggestions per target
- call `cannotLinkRepo` before admitting a candidate
- throttle based on `people_jobs` / `pending_faces_total`
- wire `MarkDirty(...)` into people mutations:
  - `MergePeople`
  - `SplitPerson`
  - `MoveFaces`
  - `UpdatePersonCategory`
  - successful clustering / reclustering paths
- have `TaskScheduler` call `RunBackgroundSlice()` on a regular ticker

**Step 4: Run test to verify it passes**

Run:

```bash
cd backend && go test -count=1 ./internal/service -run 'TestPersonMergeSuggestionService_'
cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_(MergePeopleSchedulesFeedbackReclusterAsync|FeedbackReclusterCoalescesRequests|FeedbackReclusterDefersWhileBackgroundRunning)'
```

Expected: PASS for the new service tests and no regression in the existing people feedback scheduling tests.

**Step 5: Commit**

```bash
git add backend/internal/service/person_merge_suggestion_service.go backend/internal/service/person_merge_suggestion_service_test.go backend/internal/service/service.go backend/internal/service/scheduler.go backend/internal/service/people_service.go backend/internal/service/photo_scan_service.go
git commit -m "feat: add merge suggestion background service"
```

### Task 4: Expose merge-suggestion APIs and review actions

**Files:**
- Modify: `backend/internal/model/dto.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/people_handler_test.go`
- Modify: `backend/internal/api/v1/router/router.go`

**Step 1: Write the failing test**

Extend `backend/internal/api/v1/handler/people_handler_test.go` with:

```go
func TestPeopleHandler_GetMergeSuggestionTask(t *testing.T) {}
func TestPeopleHandler_ListMergeSuggestions(t *testing.T) {}
func TestPeopleHandler_GetMergeSuggestionDetail(t *testing.T) {}
func TestPeopleHandler_ExcludeMergeSuggestionCandidates(t *testing.T) {}
func TestPeopleHandler_ApplyMergeSuggestion(t *testing.T) {}
```

Assert:

- the task / stats / logs endpoints return the dedicated task payload
- the list endpoint returns only `pending` suggestions
- exclude and apply forward the exact candidate IDs to the service
- terminal suggestion states are hidden from the list response

**Step 2: Run test to verify it fails**

Run:

```bash
cd backend && go test -count=1 ./internal/api/v1/handler -run 'TestPeopleHandler_(GetMergeSuggestionTask|ListMergeSuggestions|GetMergeSuggestionDetail|ExcludeMergeSuggestionCandidates|ApplyMergeSuggestion)'
```

Expected: FAIL because the endpoints are not registered or implemented.

**Step 3: Write minimal implementation**

Add routes under `/people/merge-suggestions` in `backend/internal/api/v1/router/router.go`:

```go
people.GET("/merge-suggestions/task", handlers.People.GetMergeSuggestionTask)
people.GET("/merge-suggestions/stats", handlers.People.GetMergeSuggestionStats)
people.GET("/merge-suggestions/background/logs", handlers.People.GetMergeSuggestionLogs)
people.POST("/merge-suggestions/background/pause", handlers.People.PauseMergeSuggestionTask)
people.POST("/merge-suggestions/background/resume", handlers.People.ResumeMergeSuggestionTask)
people.POST("/merge-suggestions/background/rebuild", handlers.People.RebuildMergeSuggestionTask)
people.GET("/merge-suggestions", handlers.People.ListMergeSuggestions)
people.GET("/merge-suggestions/:id", handlers.People.GetMergeSuggestion)
people.POST("/merge-suggestions/:id/exclude", handlers.People.ExcludeMergeSuggestionCandidates)
people.POST("/merge-suggestions/:id/apply", handlers.People.ApplyMergeSuggestion)
```

Update `PeopleHandler` to depend on the new service and implement the DTO binding / response formatting.

**Step 4: Run test to verify it passes**

Run:

```bash
cd backend && go test -count=1 ./internal/api/v1/handler -run 'TestPeopleHandler_(GetMergeSuggestionTask|ListMergeSuggestions|GetMergeSuggestionDetail|ExcludeMergeSuggestionCandidates|ApplyMergeSuggestion)'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add backend/internal/model/dto.go backend/internal/api/v1/handler/people_handler.go backend/internal/api/v1/handler/people_handler_test.go backend/internal/api/v1/router/router.go
git commit -m "feat: add merge suggestion review APIs"
```

### Task 5: Add People page suggestion UI and task controls

**Files:**
- Modify: `frontend/src/types/people.ts`
- Modify: `frontend/src/api/people.ts`
- Modify: `frontend/src/views/People/peopleHelpers.ts`
- Modify: `frontend/src/views/People/index.vue`
- Create: `frontend/src/views/People/MergeSuggestionReviewDialog.vue`
- Modify: `frontend/tests/peopleHelpers.test.ts`
- Create: `frontend/tests/peopleMergeSuggestionHelpers.test.ts`

**Step 1: Write the failing test**

Add helper tests such as:

```ts
test('getMergeSuggestionVisibility hides section when there are no pending suggestions', () => {})
test('sortMergeSuggestionCandidates orders by similarity descending', () => {})
test('getMergeSuggestionTaskStatusMeta maps paused and running states', () => {})
```

Keep the tests focused on the state transformations used by the page and dialog.

**Step 2: Run test to verify it fails**

Run:

```bash
node --test frontend/tests/peopleHelpers.test.ts frontend/tests/peopleMergeSuggestionHelpers.test.ts
```

Expected: FAIL because the helper functions and types do not exist yet.

**Step 3: Write minimal implementation**

Implement:

- new frontend types:

```ts
export interface PersonMergeSuggestionTask { status: string; current_message?: string }
export interface PersonMergeSuggestionItem { candidate_person_id: number; similarity_score: number; status: string }
export interface PersonMergeSuggestion { id: number; target_person: Person; items: PersonMergeSuggestionItem[] }
```

- API methods in `frontend/src/api/people.ts` for:
  - task / stats / logs
  - list / detail
  - pause / resume / rebuild
  - exclude / apply
- helper functions in `frontend/src/views/People/peopleHelpers.ts`
- `frontend/src/views/People/index.vue` additions:
  - a `人物合并建议` section under the people list card
  - a dedicated background-task panel for the merge-suggestion worker
  - hidden state when there are no pending suggestions
- `frontend/src/views/People/MergeSuggestionReviewDialog.vue`:
  - target summary
  - candidate checklist
  - `剔除所选`
  - `确认合并所选`

**Step 4: Run test to verify it passes**

Run:

```bash
node --test frontend/tests/peopleHelpers.test.ts frontend/tests/peopleMergeSuggestionHelpers.test.ts
cd frontend && npm run build
```

Expected: PASS.

**Step 5: Commit**

```bash
git add frontend/src/types/people.ts frontend/src/api/people.ts frontend/src/views/People/peopleHelpers.ts frontend/src/views/People/index.vue frontend/src/views/People/MergeSuggestionReviewDialog.vue frontend/tests/peopleHelpers.test.ts frontend/tests/peopleMergeSuggestionHelpers.test.ts
git commit -m "feat: add people merge suggestion review UI"
```

### Task 6: End-to-end verification and polish

**Files:**
- Modify: `backend/internal/service/person_merge_suggestion_service_test.go`
- Modify: `backend/internal/api/v1/handler/people_handler_test.go`
- Modify: `frontend/src/views/People/index.vue`
- Modify: `frontend/src/views/People/MergeSuggestionReviewDialog.vue`

**Step 1: Write the failing test**

Add a final integration-oriented backend test that:

- seeds `family`, `friend`, and `stranger` people with embeddings
- runs one background slice
- fetches the pending suggestion list
- excludes one candidate
- applies the remaining candidate(s)
- verifies:
  - `cannot-link` is written for excluded candidates
  - merged candidates disappear from pending suggestions
  - the People page section would hide once no pending suggestions remain

**Step 2: Run test to verify it fails**

Run:

```bash
cd backend && go test -count=1 ./internal/service ./internal/api/v1/handler -run 'TestPersonMergeSuggestion'
```

Expected: FAIL until the final wiring and edge cases are complete.

**Step 3: Write minimal implementation**

Polish any missing pieces revealed by the end-to-end test:

- ensure `exclude` updates both suggestion-item state and `cannot-link`
- ensure `apply` handles partial candidate sets safely
- ensure obsolete / terminal suggestions do not leak back into pending UI
- tighten People page refresh after review actions

**Step 4: Run test to verify it passes**

Run:

```bash
cd backend && go test -count=1 ./internal/service ./internal/api/v1/handler
cd backend && go test -count=1 ./...
cd frontend && npm run build
```

Expected: PASS.

**Step 5: Commit**

```bash
git add backend/internal/service/person_merge_suggestion_service_test.go backend/internal/api/v1/handler/people_handler_test.go frontend/src/views/People/index.vue frontend/src/views/People/MergeSuggestionReviewDialog.vue
git commit -m "test: verify merge suggestion flow end to end"
```
