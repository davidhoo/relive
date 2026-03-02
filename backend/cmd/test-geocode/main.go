package main

import (
	"fmt"

	"github.com/davidhoo/relive/internal/geocode"
)

func main() {
	// Test coordinates (Hangzhou West Lake)
	testCases := []struct {
		name string
		lat  float64
		lon  float64
	}{
		{"Hangzhou West Lake", 30.2741, 120.1551},
		{"Beijing Tiananmen", 39.9042, 116.4074},
		{"Shanghai Bund", 31.2397, 121.4900},
		{"New York Times Square", 40.7580, -73.9855},
	}

	// Create Nominatim provider
	provider := geocode.NewNominatimProvider(
		"https://nominatim.openstreetmap.org/reverse",
		10,
	)

	fmt.Println("Testing Geocoding Providers\n")
	fmt.Println("============================")

	for _, tc := range testCases {
		fmt.Printf("\n📍 %s (%.4f, %.4f)\n", tc.name, tc.lat, tc.lon)

		if !provider.IsAvailable() {
			fmt.Println("   ❌ Provider not available")
			continue
		}

		location, err := provider.ReverseGeocode(tc.lat, tc.lon)
		if err != nil {
			fmt.Printf("   ❌ Error: %v\n", err)
			continue
		}

		fmt.Printf("   ✅ Short: %s\n", location.FormatShort())
		fmt.Printf("   📝 Full:  %s\n", location.FormatFull())
		fmt.Printf("   ⏱️  Time:  %v\n", location.Duration)
		fmt.Printf("   🔧 Provider: %s\n", location.Provider)
	}

	fmt.Println("\n============================")
	fmt.Println("Test completed!")
}
