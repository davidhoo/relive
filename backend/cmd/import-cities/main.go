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

// GeoNames alternateNamesV2.txt 文件格式
// 0: alternateNameId
// 1: geonameid
// 2: isolanguage
// 3: alternate name
// 4: isPreferredName
// 5: isShortName
// 6: isColloquial
// 7: isHistoric
// 8: from
// 9: to

func main() {
	// 命令行参数
	filePath := flag.String("file", "", "GeoNames cities500.txt 文件路径")
	alternateNamesPath := flag.String("alternate-names", "", "GeoNames alternateNamesV2.txt 文件路径（用于导入中文城市名）")
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

	if *filePath == "" && *alternateNamesPath == "" {
		fmt.Println("使用说明:")
		fmt.Println("  go run cmd/import-cities/main.go --file cities500.txt")
		fmt.Println("  go run cmd/import-cities/main.go --alternate-names alternateNamesV2.txt  # 导入中文名")
		fmt.Println("  go run cmd/import-cities/main.go --file cities500.txt --alternate-names alternateNamesV2.txt")
		fmt.Println("  go run cmd/import-cities/main.go --check  # 检查已导入数量")
		fmt.Println("")
		fmt.Println("下载数据:")
		fmt.Println("  wget https://download.geonames.org/export/dump/cities500.zip")
		fmt.Println("  wget https://download.geonames.org/export/dump/alternateNamesV2.zip")
		fmt.Println("  unzip cities500.zip && unzip alternateNamesV2.zip")
		os.Exit(1)
	}

	// 确保 cities 表存在
	if err := db.AutoMigrate(&model.City{}); err != nil {
		logger.Fatalf("Failed to migrate cities table: %v", err)
	}

	// 导入 cities500.txt
	if *filePath != "" {
		importCities(db, *filePath, *batchSize)
	}

	// 导入中文名
	if *alternateNamesPath != "" {
		importAlternateNames(db, *alternateNamesPath, *batchSize)
	}
}

func importCities(db *gorm.DB, filePath string, batchSize int) {
	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		logger.Fatalf("Failed to open file: %v", err)
	}
	defer file.Close()

	logger.Infof("Parsing cities from %s...", filePath)

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

		for i := 0; i < len(allCities); i += batchSize {
			end := i + batchSize
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

// importAlternateNames 从 alternateNamesV2.txt 导入中文城市名到 cities 表的 name_zh 字段
func importAlternateNames(db *gorm.DB, filePath string, batchSize int) {
	// 先获取所有已导入城市的 geoname_id 集合
	var geonameIDs []int
	if err := db.Model(&model.City{}).Pluck("geoname_id", &geonameIDs).Error; err != nil {
		logger.Fatalf("Failed to get geoname IDs: %v", err)
	}
	geonameIDSet := make(map[int]bool, len(geonameIDs))
	for _, id := range geonameIDs {
		geonameIDSet[id] = true
	}
	logger.Infof("Found %d cities in database, loading alternate names...", len(geonameIDs))

	// 解析 alternateNamesV2.txt，提取 zh 语言的首选名称
	file, err := os.Open(filePath)
	if err != nil {
		logger.Fatalf("Failed to open alternate names file: %v", err)
	}
	defer file.Close()

	// geonameID -> 中文名（优先级：zh-CN > zh > zh-TW）
	zhNames := make(map[int]string)
	zhPriority := make(map[int]int) // 已选名称的优先级

	scanner := bufio.NewScanner(file)
	// alternateNamesV2.txt 行可能很长
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	lineCount := 0
	matchCount := 0

	for scanner.Scan() {
		line := scanner.Text()
		lineCount++

		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}

		// fields[2] = isolanguage，匹配 zh、zh-CN、zh-TW
		lang := fields[2]
		var priority int
		switch lang {
		case "zh-CN":
			priority = 3
		case "zh":
			priority = 2
		case "zh-TW":
			priority = 1
		default:
			continue
		}

		geonameID, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}

		// 只处理我们数据库中有的城市
		if !geonameIDSet[geonameID] {
			continue
		}

		name := strings.TrimSpace(fields[3])
		if name == "" {
			continue
		}

		isPreferred := len(fields) > 4 && fields[4] == "1"

		// 同优先级内，preferred 优先；高优先级语言直接覆盖低优先级
		existingPri := zhPriority[geonameID]
		if priority < existingPri {
			continue
		}
		if priority == existingPri && !isPreferred {
			continue
		}

		zhNames[geonameID] = name
		zhPriority[geonameID] = priority
		matchCount++
	}

	if err := scanner.Err(); err != nil {
		logger.Fatalf("Error reading alternate names file: %v", err)
	}

	logger.Infof("Parsed %d lines, found %d zh names for %d cities", lineCount, matchCount, len(zhNames))

	if len(zhNames) == 0 {
		logger.Info("No Chinese names found, skipping update")
		return
	}

	// 批量更新 name_zh
	updatedCount := 0
	type updateItem struct {
		geonameID int
		nameZH    string
	}
	var items []updateItem
	for id, name := range zhNames {
		items = append(items, updateItem{geonameID: id, nameZH: name})
	}

	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		batch := items[i:end]

		if err := db.Transaction(func(tx *gorm.DB) error {
			for _, item := range batch {
				if err := tx.Model(&model.City{}).
					Where("geoname_id = ?", item.geonameID).
					Update("name_zh", item.nameZH).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			logger.Fatalf("Failed to update Chinese names at offset %d: %v", i, err)
		}

		updatedCount += len(batch)
		if updatedCount%5000 == 0 || updatedCount == len(items) {
			logger.Infof("Updated %d/%d Chinese city names...", updatedCount, len(items))
		}
	}

	logger.Infof("Chinese city names import completed: %d cities updated", updatedCount)
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
