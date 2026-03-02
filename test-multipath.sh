#!/bin/bash

# Multi-Path Management Test Script
# This script helps verify the implementation works correctly

BASE_URL="${BASE_URL:-http://localhost:8080/api/v1}"

echo "=================================="
echo "Multi-Path Management Test Script"
echo "=================================="
echo ""

# Test 1: Validate a path
echo "Test 1: Validating path /tmp"
curl -s -X POST "$BASE_URL/photos/validate-path" \
  -H "Content-Type: application/json" \
  -d '{"path": "/tmp"}' | jq '.'
echo ""

# Test 2: Set scan paths config
echo "Test 2: Setting scan paths configuration"
SCAN_PATHS='{
  "paths": [
    {
      "id": "test-path-1",
      "name": "Test Path 1",
      "path": "/tmp",
      "is_default": true,
      "enabled": true,
      "created_at": "'$(date -u +"%Y-%m-%dT%H:%M:%SZ")'"
    }
  ]
}'

curl -s -X PUT "$BASE_URL/config/photos.scan_paths" \
  -H "Content-Type: application/json" \
  -d "{\"value\": $(echo "$SCAN_PATHS" | jq -c '@json')}" | jq '.'
echo ""

# Test 3: Get scan paths config
echo "Test 3: Getting scan paths configuration"
curl -s -X GET "$BASE_URL/config/photos.scan_paths" | jq '.'
echo ""

# Test 4: Scan without specifying path (should use default)
echo "Test 4: Scanning photos without specifying path (uses default)"
curl -s -X POST "$BASE_URL/photos/scan" \
  -H "Content-Type: application/json" \
  -d '{}' | jq '.'
echo ""

# Test 5: Verify last_scanned_at was updated
echo "Test 5: Verifying last_scanned_at was updated"
curl -s -X GET "$BASE_URL/config/photos.scan_paths" | jq '.data | fromjson | .paths[0].last_scanned_at'
echo ""

echo "=================================="
echo "Tests completed!"
echo "=================================="
