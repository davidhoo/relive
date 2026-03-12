package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// GeoNames cities500.txt 文件格式
// 0: geonameid
// 1: name
// 2: asciiname
// 3: alternatenames
// 4: latitude
// 5: longitude
// 6: feature class
// 7: feature code
// 8: country code
// 9: cc2
// 10: admin1 code
// 11: admin2 code
// 12: admin3 code
// 13: admin4 code
// 14: population
// 15: elevation
// 16: dem
// 17: timezone
// 18: modification date

func main() {
	// 命令行参数
	filePath := flag.String("file", "", "GeoNames cities500.txt 文件路径")
	configPath := flag.String("config", "config.dev.yaml", "配置文件路径")
	batchSize := flag.Int("batch", 1000, "批量插入大小")
	checkOnly := flag.Bool("check", false, "仅检查数据库中的城市数量")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 初始化日志
	if err := logger.Init(cfg.Logging); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	// 初始化数据库
	db, err := database.Init(cfg.Database)
	if err != nil {
		logger.Fatalf("Failed to initialize database: %v", err)
	}

	// 如果是检查模式，只返回数量
	if *checkOnly {
		var count int64
		if err := db.Model(&model.City{}).Count(&count).Error; err != nil {
			fmt.Println("0")
			os.Exit(1)
		}
		fmt.Println(count)
		os.Exit(0)
	}

	if *filePath == "" {
		fmt.Println("使用说明:")
		fmt.Println("  go run cmd/import-cities/main.go --file cities500.txt")
		fmt.Println("  go run cmd/import-cities/main.go --check  # 检查已导入数量")
		fmt.Println("")
		fmt.Println("下载数据:")
		fmt.Println("  wget https://download.geonames.org/export/dump/cities500.zip")
		fmt.Println("  unzip cities500.zip")
		os.Exit(1)
	}

	// 确保 cities 表存在
	if err := db.AutoMigrate(&model.City{}); err != nil {
		logger.Fatalf("Failed to migrate cities table: %v", err)
	}

	// 打开文件
	file, err := os.Open(*filePath)
	if err != nil {
		logger.Fatalf("Failed to open file: %v", err)
	}
	defer file.Close()

	logger.Infof("Parsing cities from %s...", *filePath)

	// 先解析全部数据
	scanner := bufio.NewScanner(file)
	var allCities []model.City
	totalCount := 0
	skippedCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		totalCount++

		city, err := parseLine(line)
		if err != nil {
			skippedCount++
			if skippedCount <= 10 {
				logger.Warnf("Skipping line %d: %v", totalCount, err)
			}
			continue
		}

		allCities = append(allCities, *city)
	}

	if err := scanner.Err(); err != nil {
		logger.Fatalf("Error reading file: %v", err)
	}

	logger.Infof("Parsed %d cities from %d lines (skipped: %d), importing...", len(allCities), totalCount, skippedCount)

	// 在事务中执行清空和批量插入，确保原子性
	insertedCount := 0
	if err := db.Transaction(func(tx *gorm.DB) error {
		logger.Info("Clearing existing city data...")
		if err := tx.Exec("DELETE FROM cities").Error; err != nil {
			return fmt.Errorf("failed to clear cities table: %w", err)
		}

		for i := 0; i < len(allCities); i += *batchSize {
			end := i + *batchSize
			if end > len(allCities) {
				end = len(allCities)
			}
			batch := allCities[i:end]
			if err := tx.Create(&batch).Error; err != nil {
				return fmt.Errorf("failed to insert batch at offset %d: %w", i, err)
			}
			insertedCount += len(batch)
			logger.Infof("Imported %d cities...", insertedCount)
		}

		return nil
	}); err != nil {
		logger.Fatalf("Import failed: %v", err)
	}

	logger.Infof("Import completed!")
	logger.Infof("Total lines: %d", totalCount)
	logger.Infof("Imported: %d", insertedCount)
	logger.Infof("Skipped: %d", skippedCount)

	// 显示统计
	var count int64
	db.Model(&model.City{}).Count(&count)
	logger.Infof("Cities in database: %d", count)
}

func parseLine(line string) (*model.City, error) {
	fields := strings.Split(line, "\t")
	if len(fields) < 19 {
		return nil, fmt.Errorf("invalid line format: expected 19 fields, got %d", len(fields))
	}

	// 解析 geonameid
	geonameID, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("invalid geoname_id: %s", fields[0])
	}

	// 解析纬度
	latitude, err := strconv.ParseFloat(fields[4], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid latitude: %s", fields[4])
	}

	// 解析经度
	longitude, err := strconv.ParseFloat(fields[5], 64)
	if err != nil {
		return nil, fmt.Errorf("invalid longitude: %s", fields[5])
	}

	// 获取 admin1 (省/州) 名称
	// 注意: GeoNames 的 admin1 code 需要额外查询转换为名称
	// 这里我们直接使用 admin2 或者留空，后续可以通过 admin1Codes.txt 完善
	adminName := fields[10] // admin1 code，可以后续映射为名称

	city := &model.City{
		GeonameID: geonameID,
		Name:      fields[1], // name
		AdminName: adminName, // admin1 code (可以后续改进)
		Country:   fields[8], // country code
		Latitude:  latitude,
		Longitude: longitude,
	}

	return city, nil
}
