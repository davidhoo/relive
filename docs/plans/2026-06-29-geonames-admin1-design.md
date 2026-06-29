# GeoNames China Admin1 Mapping Fix Design

## Problem

The embedded city dataset stores GeoNames `admin1` codes. The runtime mapping in `internal/geocode/offline.go` instead treats those values as a sequential China province list, so valid coordinates can be assigned to the wrong province. For example, GeoNames code `02` is Zhejiang, while the current code maps it to Tianjin.

## Design

Replace the complete China province mapping with the authoritative GeoNames `CN.*` admin1 mapping. Keep the embedded dataset and database schema unchanged so existing installations receive the correction immediately after upgrading.

Add table-driven coverage for every supported China admin1 code and a provider-level regression test for Issue #11 using `(30.292125, 120.378533)`. The regression test will verify that the nearest `Xiasha` city row produces `浙江省Xiasha`, proving both nearest-city lookup and province formatting use the corrected mapping.

## Existing Data

Already-geocoded photos retain their stored location fields. After deployment, users must run the existing full GPS location rebuild so those records are recalculated with the corrected mapping.
