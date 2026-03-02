# GPS Reverse Geocoding System

## Overview

The Relive backend now includes a robust multi-provider GPS reverse geocoding system that automatically converts GPS coordinates (latitude/longitude) to human-readable location names (city, province, country) during photo scanning.

## Architecture

### Provider Pattern

The system uses a **provider pattern** similar to the AI service, allowing multiple geocoding providers with automatic fallback:

```
GPS Coordinates (lat, lon)
         ↓
   GeocodeService
         ↓
   [Provider Priority Queue]
         ↓
   ┌─────┴──────┬──────────┬───────────┐
   │            │          │           │
Offline      AMap    Nominatim    (Future)
Provider   Provider   Provider    Providers
```

### Components

#### 1. **Provider Interface** (`internal/geocode/provider.go`)
- Defines the contract all geocoding providers must implement
- `ReverseGeocode(lat, lon float64)` - Main geocoding method
- `IsAvailable()` - Health check
- `Priority()` - Determines fallback order (lower = higher priority)
- `Name()` - Provider identifier

#### 2. **Service Layer** (`internal/geocode/service.go`)
- Manages multiple providers
- Implements automatic fallback logic
- Built-in caching with TTL
- Sorts providers by priority

#### 3. **Available Providers**

##### **Offline Provider** (`internal/geocode/offline.go`)
- **Priority:** 5 (highest - fastest, no API limits)
- **Data Source:** Local SQLite database (`cities` table)
- **Algorithm:** Haversine distance calculation
- **Range:** Configurable maximum distance (default 100km)
- **Advantages:**
  - Zero latency
  - No API keys needed
  - No rate limits
  - Works offline
- **Limitations:**
  - Requires city database to be populated
  - Lower accuracy than online services
  - City-level precision only

##### **AMap Provider (高德地图)** (`internal/geocode/amap.go`)
- **Priority:** 10 (medium - excellent for China)
- **Data Source:** Gaode Maps API (restapi.amap.com)
- **API Key Required:** Yes
- **Advantages:**
  - Excellent coverage in China
  - Detailed location data (district level)
  - Official Chinese location names
  - Fast response times in China
- **Limitations:**
  - Requires API key
  - API rate limits apply
  - Best suited for Chinese locations

##### **Nominatim Provider (OpenStreetMap)** (`internal/geocode/nominatim.go`)
- **Priority:** 20 (lowest - global coverage but slower)
- **Data Source:** OpenStreetMap Nominatim API
- **API Key Required:** No
- **Advantages:**
  - Global coverage
  - Free and open source
  - No API key needed
  - Respects privacy
- **Limitations:**
  - Rate limited to 1 request/second
  - Slower response times
  - Lower priority due to rate limits

## Configuration

Add to your `config.yaml`:

```yaml
geocode:
  provider: "offline"           # Primary provider: offline / amap / nominatim
  fallback: "nominatim"         # Fallback provider (optional)
  cache_enabled: true           # Enable response caching
  cache_ttl: 86400             # Cache TTL in seconds (24 hours)

  # AMap (高德地图) Configuration
  amap_api_key: ""              # Get from: https://lbs.amap.com/
  amap_timeout: 10              # Request timeout in seconds

  # Nominatim (OpenStreetMap) Configuration
  nominatim_endpoint: "https://nominatim.openstreetmap.org/reverse"
  nominatim_timeout: 10         # Request timeout in seconds

  # Offline Configuration
  offline_max_distance: 100     # Maximum search radius in km
```

### Development Configuration

The `config.dev.yaml` is pre-configured with:
- **Primary:** Offline (fast, no API required)
- **Fallback:** Nominatim (free, global coverage)
- **Cache:** Enabled with 24-hour TTL

This setup works out-of-the-box for development without any API keys.

### Production Recommendations

For production deployments in China:
```yaml
geocode:
  provider: "amap"              # Best for China
  fallback: "offline"           # Fast offline fallback
  amap_api_key: "your-key-here"
```

For global deployments:
```yaml
geocode:
  provider: "nominatim"         # Global coverage
  fallback: "offline"           # Offline fallback
```

## Integration

### Photo Scanning Workflow

The geocoding happens automatically during photo scanning:

1. **EXIF Extraction** - GPS coordinates extracted from photo metadata
2. **Geocoding Check** - If GPS coordinates exist (non-null):
   ```go
   if photo.GPSLatitude != nil && photo.GPSLongitude != nil {
       location := geocodeService.ReverseGeocode(*photo.GPSLatitude, *photo.GPSLongitude)
       photo.Location = location.FormatShort()
   }
   ```
3. **Provider Fallback** - Service tries providers in priority order:
   - Offline → AMap → Nominatim (if all configured)
4. **Caching** - Successful results cached to avoid redundant lookups
5. **Database Storage** - Location stored in `photos.location` field

### Location Format

The `Location` struct provides two formatting methods:

**FormatShort()** - Concise format (stored in database)
```
城市名 + 区县名
Example: "杭州市西湖区"
```

**FormatFull()** - Complete format (for display)
```
国家 省份 城市 区县
Example: "中国 浙江省 杭州市 西湖区"
```

## Database Schema

### Photos Table
```sql
ALTER TABLE photos ADD COLUMN location VARCHAR(200);
```

### Cities Table (for Offline Provider)
```sql
CREATE TABLE cities (
    id INTEGER PRIMARY KEY,
    geoname_id INTEGER UNIQUE NOT NULL,
    name VARCHAR(200) NOT NULL,
    admin_name VARCHAR(200),      -- Province/State
    country VARCHAR(100) NOT NULL,
    latitude REAL NOT NULL,
    longitude REAL NOT NULL
);

CREATE INDEX idx_geoname_id ON cities(geoname_id);
CREATE INDEX idx_name ON cities(name);
CREATE INDEX idx_lat ON cities(latitude);
CREATE INDEX idx_lon ON cities(longitude);
```

## Populating City Database

For the offline provider to work, you need to populate the `cities` table. Options:

### Option 1: GeoNames Dataset
1. Download from http://www.geonames.org/
2. Use `cities15000.txt` (cities with 15000+ population)
3. Import script example:
   ```bash
   # TODO: Create importer tool
   go run cmd/import-cities/main.go --file cities15000.txt
   ```

### Option 2: Custom City List
- Add your own curated list of important cities
- Especially useful for specific regions

### Option 3: Skip Offline Provider
- Just use AMap or Nominatim
- Set `provider: "nominatim"` in config

## Performance Characteristics

| Provider   | Latency | Rate Limit      | Coverage | Accuracy | Cost  |
|------------|---------|-----------------|----------|----------|-------|
| Offline    | ~1ms    | Unlimited       | Limited  | City     | Free  |
| AMap       | ~100ms  | 10,000/day free | China++  | District | Paid* |
| Nominatim  | ~500ms  | 1 req/sec       | Global   | District | Free  |

*AMap offers free tier with daily limits

## Monitoring

The service logs all geocoding attempts:

```
INFO  Geocode service initialized with providers: [offline nominatim]
DEBUG Trying provider: offline
DEBUG Offline geocode: (30.2741,120.1551) -> 杭州市 (2.3 km, took 892µs)
INFO  Geocoded: (30.2741,120.1551) -> 杭州市西湖区
```

Failed attempts are logged as warnings:
```
WARN  Geocode failed for (30.2741, 120.1551): no nearby city found within 100 km
```

## Error Handling

The system gracefully handles errors:
- **No providers available** - Photo saved with GPS but no location
- **All providers fail** - Warning logged, GPS coordinates preserved
- **Invalid coordinates** - Skipped silently
- **Provider timeout** - Automatically falls back to next provider

The photo scanning process **never fails** due to geocoding issues.

## Future Enhancements

Potential additions:
- **rgeo** - Ruby geocoding library
- **Photon** - Another OSM-based geocoder
- **Google Maps Geocoding API** - Premium option
- **Batch geocoding** - Process multiple coordinates at once
- **Custom region databases** - Country-specific optimizations
- **Reverse proxy** - Cache Nominatim responses locally

## API Endpoints

Currently geocoding is automatic during photo scanning. Future endpoints could include:

- `POST /api/v1/geocode/reverse` - Manual reverse geocoding
- `POST /api/v1/photos/:id/geocode` - Re-geocode existing photo
- `POST /api/v1/photos/batch-geocode` - Batch update all photos with GPS
- `GET /api/v1/geocode/providers` - List available providers
- `GET /api/v1/geocode/stats` - Cache hit rates, provider usage

## Testing

### Unit Tests
```bash
go test ./internal/geocode/...
```

### Integration Test
```bash
# Start server
./bin/relive -config config.dev.yaml

# Scan photos with GPS data
curl -X POST http://localhost:8080/api/v1/photos/scan \
  -H "Content-Type: application/json" \
  -d '{"path": "/path/to/photos/with/gps"}'

# Check results
curl http://localhost:8080/api/v1/photos?analyzed=false | jq '.data[].location'
```

## Troubleshooting

### "No geocode providers available"
- Check config file has at least one provider configured
- Verify API keys are set (for AMap)
- Ensure city database populated (for offline)

### "All providers failed"
- Check network connectivity (for online providers)
- Verify API key validity (for AMap)
- Check rate limits (for Nominatim)
- Verify GPS coordinates are valid

### Empty location field
- Photo may not have GPS data
- Check EXIF metadata: `exiftool -GPS* photo.jpg`
- Verify location services were enabled when photo was taken

### Slow performance
- Enable caching: `cache_enabled: true`
- Use offline provider as primary
- Batch process photos during off-peak hours

## References

- [高德地图 API 文档](https://lbs.amap.com/api/webservice/guide/api/georegeo)
- [Nominatim API 文档](https://nominatim.org/release-docs/latest/api/Reverse/)
- [GeoNames 数据集](http://www.geonames.org/)
- [Haversine Distance Formula](https://en.wikipedia.org/wiki/Haversine_formula)
