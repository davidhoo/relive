// gen-geodata is a one-time tool that preprocesses GeoNames data files
// into a compact, merged format for embedding in the binary.
//
// Usage:
//
//	go run cmd/gen-geodata/main.go \
//	  -cities cities500.txt \
//	  -alt alternateNamesV2.txt \
//	  -out pkg/geodata/cities_zh.csv.gz
package main

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

type city struct {
	GeonameID int
	Name      string
	Latitude  float64
	Longitude float64
	Country   string
	Admin1    string
}

func main() {
	citiesFile := flag.String("cities", "cities500.txt", "path to GeoNames cities500.txt")
	altFile := flag.String("alt", "alternateNamesV2.txt", "path to GeoNames alternateNamesV2.txt")
	outFile := flag.String("out", "pkg/geodata/cities_zh.csv.gz", "output gzip TSV file")
	flag.Parse()

	// Step 1: Parse cities500.txt
	log.Printf("Reading cities from %s ...", *citiesFile)
	cities, idIndex := parseCities(*citiesFile)
	log.Printf("Loaded %d cities", len(cities))

	// Step 2: Parse alternateNamesV2.txt for zh names
	log.Printf("Reading alternate names from %s ...", *altFile)
	zhNames := parseAlternateNames(*altFile, idIndex)
	log.Printf("Found %d Chinese city names", len(zhNames))

	// Step 3: Write output
	log.Printf("Writing output to %s ...", *outFile)
	writeOutput(*outFile, cities, zhNames)
	log.Println("Done!")
}

func parseCities(path string) ([]city, map[int]bool) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open cities file: %v", err)
	}
	defer f.Close()

	var cities []city
	idIndex := make(map[int]bool)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if len(fields) < 19 {
			continue
		}

		geonameID, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		lat, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			continue
		}
		lon, err := strconv.ParseFloat(fields[5], 64)
		if err != nil {
			continue
		}

		cities = append(cities, city{
			GeonameID: geonameID,
			Name:      fields[1],
			Latitude:  lat,
			Longitude: lon,
			Country:   fields[8],
			Admin1:    fields[10],
		})
		idIndex[geonameID] = true
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("scan cities: %v", err)
	}
	return cities, idIndex
}

// parseAlternateNames reads alternateNamesV2.txt and extracts Chinese names.
// Priority: zh-CN > zh > zh-TW (higher priority overwrites lower).
func parseAlternateNames(path string, cityIDs map[int]bool) map[int]string {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open alternate names file: %v", err)
	}
	defer f.Close()

	// priority: zh-CN=3, zh=2, zh-TW=1
	type entry struct {
		name     string
		priority int
	}

	best := make(map[int]entry)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineCount := 0

	for scanner.Scan() {
		lineCount++
		if lineCount%5000000 == 0 {
			log.Printf("  processed %dM lines...", lineCount/1000000)
		}

		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}

		lang := fields[2]
		var prio int
		switch lang {
		case "zh-CN":
			prio = 3
		case "zh":
			prio = 2
		case "zh-TW":
			prio = 1
		default:
			continue
		}

		geonameID, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		if !cityIDs[geonameID] {
			continue
		}

		name := strings.TrimSpace(fields[3])
		if name == "" {
			continue
		}

		if cur, ok := best[geonameID]; !ok || prio > cur.priority {
			best[geonameID] = entry{name: name, priority: prio}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("scan alternate names: %v", err)
	}

	result := make(map[int]string, len(best))
	for id, e := range best {
		result[id] = e.name
	}
	return result
}

func writeOutput(path string, cities []city, zhNames map[int]string) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("create output file: %v", err)
	}
	defer f.Close()

	gw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		log.Fatalf("create gzip writer: %v", err)
	}
	defer gw.Close()

	w := bufio.NewWriter(gw)
	for _, c := range cities {
		nameZH := zhNames[c.GeonameID]
		fmt.Fprintf(w, "%d\t%s\t%s\t%.5f\t%.5f\t%s\t%s\n",
			c.GeonameID, c.Name, nameZH, c.Latitude, c.Longitude, c.Country, c.Admin1)
	}
	if err := w.Flush(); err != nil {
		log.Fatalf("flush: %v", err)
	}
}
