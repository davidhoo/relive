package geodata

import (
	"bufio"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

//go:embed cities_zh.csv.gz
var citiesData []byte

// EnsureCitiesLoaded checks if the cities table has data.
// If not (or less than 1000 rows), it imports from the embedded dataset.
func EnsureCitiesLoaded(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.City{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count cities: %w", err)
	}
	if count >= 1000 {
		log.Printf("[geodata] cities table has %d rows, skipping import", count)
		return nil
	}

	log.Printf("[geodata] cities table has %d rows, importing embedded data...", count)
	return ImportEmbeddedCities(db)
}

// ImportEmbeddedCities imports all cities from embedded data into the database.
// It deletes existing data and replaces it in a single transaction.
func ImportEmbeddedCities(db *gorm.DB) error {
	cities, err := parseEmbeddedData()
	if err != nil {
		return fmt.Errorf("parse embedded data: %w", err)
	}

	log.Printf("[geodata] parsed %d cities from embedded data, importing...", len(cities))

	const batchSize = 1000
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM cities").Error; err != nil {
			return fmt.Errorf("delete cities: %w", err)
		}

		for i := 0; i < len(cities); i += batchSize {
			end := i + batchSize
			if end > len(cities) {
				end = len(cities)
			}
			if err := tx.Create(cities[i:end]).Error; err != nil {
				return fmt.Errorf("batch insert cities [%d:%d]: %w", i, end, err)
			}
		}

		log.Printf("[geodata] imported %d cities successfully", len(cities))
		return nil
	})
}

func parseEmbeddedData() ([]model.City, error) {
	r, err := gzip.NewReader(strings.NewReader(string(citiesData)))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer r.Close()

	var cities []model.City
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 512*1024), 512*1024)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}

		geonameID, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		lat, err := strconv.ParseFloat(fields[3], 64)
		if err != nil {
			continue
		}
		lon, err := strconv.ParseFloat(fields[4], 64)
		if err != nil {
			continue
		}

		cities = append(cities, model.City{
			GeonameID: geonameID,
			Name:      fields[1],
			NameZH:    fields[2],
			Latitude:  lat,
			Longitude: lon,
			Country:   fields[5],
			AdminName: fields[6],
		})
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return nil, fmt.Errorf("scan: %w", err)
	}

	return cities, nil
}
