# Router Bridge For Request Redirects Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** Remove the router dynamic-import warning by decoupling `request.ts` from direct router module imports while preserving the existing 401/403 redirect behavior.

**Architecture:** Add a tiny router bridge module that stores the app router instance after bootstrap. `main.ts` registers the router once, and `request.ts` calls bridge helpers instead of dynamically importing `@/router`.

**Tech Stack:** Vue 3, TypeScript, Vite, Axios, Vue Router

---

### Task 1: Add a failing structure check for the bridge pattern

**Files:**
- Create: `frontend/scripts/check-router-bridge.mjs`
- Test: `frontend/src/utils/request.ts`
- Test: `frontend/src/main.ts`

**Step 1: Write the failing check**

Add a small Node script that asserts:

- `frontend/src/router/bridge.ts` exists
- `frontend/src/utils/request.ts` does not contain `import('@/router')`
- `frontend/src/main.ts` registers the router bridge

**Step 2: Run check to verify it fails**

Run:
```bash
cd frontend && node scripts/check-router-bridge.mjs
```

Expected: FAIL because the bridge file and registration do not exist yet.

### Task 2: Implement the router bridge

**Files:**
- Create: `frontend/src/router/bridge.ts`
- Modify: `frontend/src/main.ts`
- Modify: `frontend/src/utils/request.ts`

**Step 1: Write minimal implementation**

- add `registerRouter(router)` and `navigateTo(path)` helpers
- register the app router in `main.ts`
- replace dynamic router import in `request.ts` with bridge calls

**Step 2: Run the structure check**

Run:
```bash
cd frontend && node scripts/check-router-bridge.mjs
```

Expected: PASS

### Task 3: Verify production build output

**Files:**
- No code changes expected

**Step 1: Run build**

Run:
```bash
cd frontend && npm run build
```

Expected:
- PASS
- the specific Vite warning about `src/router/index.ts` being both dynamically and statically imported is gone

### Task 4: Final verification

**Files:**
- No code changes expected

**Step 1: Run both checks**

Run:
```bash
cd frontend && node scripts/check-router-bridge.mjs
cd frontend && npm run build
```

Expected: PASS
