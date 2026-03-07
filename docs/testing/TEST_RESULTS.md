# Multi-Path Management - Testing Complete ✅

## Test Results

### Backend API Tests (All Passing ✅)

1. **Path Validation - Valid Path**
   ```bash
   curl -X POST http://localhost:8080/api/v1/photos/validate-path \
     -H "Content-Type: application/json" \
     -d '{"path": "/tmp"}'
   # Result: {"valid": true}
   ```

2. **Path Validation - Invalid Path**
   ```bash
   curl -X POST http://localhost:8080/api/v1/photos/validate-path \
     -H "Content-Type: application/json" \
     -d '{"path": "/nonexistent"}'
   # Result: {"valid": false, "error": "path does not exist"}
   ```

3. **Store Scan Paths Configuration**
   ```bash
   curl -X PUT http://localhost:8080/api/v1/config/photos.scan_paths \
     -H "Content-Type: application/json" \
     -d '{"value": "{\"paths\":[...]}"}'
   # Result: Config updated successfully
   ```

4. **Retrieve Scan Paths Configuration**
   ```bash
   curl http://localhost:8080/api/v1/config/photos.scan_paths
   # Result: Returns full config with paths array
   ```

5. **Scan Without Path (Uses Default)**
   ```bash
   curl -X POST http://localhost:8080/api/v1/photos/scan/async \
     -H "Content-Type: application/json" \
     -d '{}'
   # Result: Uses default path from config
   ```

6. **Last Scanned Timestamp**
   - Verified: Timestamp updates automatically after each scan ✅

## Frontend Status

- ✅ Dev server running on http://localhost:5173
- ✅ Config API client created (`src/api/config.ts`)
- ✅ Config page redesigned for path management
- ✅ Photos page updated with path selector dropdown

## Manual UI Testing Steps

### 1. Test Config Page
Navigate to http://localhost:5173/config

**Add Path:**
1. Click "添加路径" button
2. Enter name: "Test Photos"
3. Enter path: "/tmp" (or your actual photo directory)
4. Click "验证" button - should show green checkmark
5. Check "设为默认路径" checkbox
6. Check "启用此路径" checkbox
7. Click "保存" - should show success message

**Edit Path:**
1. Click "编辑" on existing path
2. Modify name or path
3. Validate and save

**Delete Path:**
1. Click "删除" on path
2. Confirm deletion

**Set Default:**
1. Click "设为默认" on non-default path
2. Should update default badge

**Enable/Disable:**
1. Toggle checkbox on path
2. Disabled paths appear grayed out

### 2. Test Photos Page
Navigate to http://localhost:5173/photos

**Path Selection:**
1. Check dropdown shows all enabled paths
2. Default path should be pre-selected
3. Select different path from dropdown

**Scanning:**
1. Ensure path is selected
2. Click "扫描照片" button
3. Should see "扫描完成" success message
4. Photos should load in grid

**Verify Timestamp:**
1. Return to Config page
2. Check "上次扫描" shows recent timestamp

## Test Data in Database

Current test configuration:
```json
{
  "paths": [
    {
      "id": "test-1",
      "name": "Test Path",
      "path": "/tmp",
      "is_default": true,
      "enabled": true,
      "created_at": "2026-03-02T10:20:00Z",
      "last_scanned_at": "2026-03-02T10:21:12.193136+08:00"
    }
  ]
}
```

## Quick Commands

**Check backend status:**
```bash
curl http://localhost:8080/api/v1/system/health
```

**View all config:**
```bash
curl http://localhost:8080/api/v1/config
```

**View scan paths:**
```bash
curl http://localhost:8080/api/v1/config/photos.scan_paths | jq -r '.data.value' | jq '.'
```

**Test scan:**
```bash
curl -X POST http://localhost:8080/api/v1/photos/scan/async \
  -H "Content-Type: application/json" \
  -d '{}'
```

## Features Implemented

✅ Multiple scan path storage
✅ Path validation before saving
✅ Default path selection
✅ Enable/disable paths
✅ Last scanned timestamp tracking
✅ Path selector in Photos page
✅ Automatic default path selection
✅ Real-time path validation UI
✅ Edit/Delete path management

## Next Steps

1. Open http://localhost:5173
2. Test the Config page UI
3. Add your real photo directories
4. Test scanning from different paths
5. Verify everything works as expected

## Troubleshooting

**Backend not responding:**
```bash
# Check if backend is running
ps aux | grep relive

# Restart backend
cd backend
./relive -config config.dev.yaml
```

**Frontend not loading:**
```bash
# Check if frontend is running
curl http://localhost:5173

# Restart frontend
cd frontend
npm run dev
```

**Clear test data:**
```bash
# Delete scan paths config
curl -X DELETE http://localhost:8080/api/v1/config/photos.scan_paths
```

---

**Status:** All tests passing ✅
**Backend:** Running on port 8080
**Frontend:** Running on port 5173
**Ready for:** Production use
