# Delivery Report: people.identity_profile_mode primary

Date: 2026-09-08  
Branch: `feature/people-identity-primary`  
Worktree: `.worktrees/people-identity-primary`  
Plan: `docs/plans/2026-09-08-people-identity-primary.md`

## Status summary

| Gate | Status |
| --- | --- |
| Code complete (local branch) | Yes — primary takeover implemented |
| Backend tests | `go test ./...` passed |
| Frontend typecheck | vue-tsc invoked (exit 0); UI fields added |
| Deployed | No |
| Config switched to primary | No (defaults unchanged) |
| Real profile attach observed | No production evidence |
| New suggestions use unified engine | Code path ready; not production-verified |
| NAS baseline / replica validation | Missing — not run in this session |

## What was implemented

### Unified identity matching engine
- `identity_matching_types.go` / `score.go` / `strategy.go` / `engine.go`
- Shared scoring (`ScoreIdentityEvidence`), status taxonomy (match / no_candidate / insufficient / hard_conflict / unavailable / invalid)
- Auto vs suggest strategies; fingerprint; config validation `rescue_threshold >= merge_suggestion_threshold`

### Primary clustering
- `people_identity_primary.go`: decision table, technical wait backoff, no legacy fallback
- `runIncrementalClustering` branches to primary when engine injected
- Test: `TestPeopleService_IdentityProfilePrimary_OverridesLegacyHit` (legacy would pick A, primary writes B)

### Primary merge suggestions
- `primaryAssignments` path — no batch/target/pair legacy fallback
- Hard conflicts dropped; unavailable targets surface as partial task message
- Suggestion metadata fields + review UI for stale/reason/engine/source

### Assignment log + revoke API
- Models + migration `migration.people_identity_primary_v1` + assignment_version trigger
- Batch create/finalize on coordinator; change rows on primary attach/create
- Admin APIs:
  - `GET /people/identity-assignment-batches`
  - `GET /people/identity-assignment-batches/:id`
  - `POST .../revoke/preview`
  - `POST .../revoke`
- Test: version-conflict skip + revoke idempotency

### Docs
- `docs/PEOPLE_IDENTITY_PROFILE_ROLLOUT.md` §8 primary activate/rollback
- `README.md` primary mode wording updated

## Known gaps / follow-ups

1. **Accept-time revalidation** for ApplySuggestion under primary (engine re-compare + generation check) is not fully wired.
2. **Automatic stale-marking** of pending hybrid/legacy suggestions on mode transition is not a dedicated startup job yet (UI can show stale when fields set).
3. **Replica/NAS baseline comparison** (rescue vs primary) was not executed — missing real-environment evidence.
4. Not every plan §6 case has a dedicated test; core override / strategy / hard-conflict / unavailable-vs-empty / assignment revoke conflict are covered.
5. Production default remains `legacy`; enabling primary requires explicit config + restart per rollout doc.

## Suggested next steps (needs your authorization)

1. Review/merge branch
2. Deploy migration on staging/copy DB
3. Switch `identity_profile_mode: primary` only after backup + smoke
4. Confirm assignment batches and suggestion `engine_version` / `match_source`
