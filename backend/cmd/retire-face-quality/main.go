// Package main 实现 retire-face-quality 离线迁移入口。
//
// 默认只读预演（-apply 未指定时绝不写入）。显式 -apply 才执行变更。
// 不调用检测/复核模型，不触发全库重聚类。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidhoo/relive/internal/service"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type options struct {
	dbPath   string
	apply    bool
	out      string
	mirror   string
	planPath string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args, stderr)
	if err != nil {
		return 2
	}

	db, err := openDB(opts.dbPath, opts.apply)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	sqlDB, _ := db.DB()
	if sqlDB != nil {
		defer sqlDB.Close()
	}

	var plan *service.RetirementPlan
	if opts.apply {
		plan, err = readApprovedPlan(opts.planPath)
	} else {
		plan, err = service.PlanFaceQualityRetirement(db)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: plan: %v\n", err)
		return 1
	}

	if !opts.apply {
		if err := writePlan(opts.out, plan, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "dry-run only (no writes). summary=%v\n", plan.Summary)
		fmt.Fprintf(stderr, "unknown=%d must NOT be counted as cleaned. use -apply to write.\n", plan.Summary[service.RetirementCategoryUnknown])
		return 0
	}

	if err := writePlan(opts.mirror, plan, stdout); err != nil {
		fmt.Fprintf(stderr, "error: write mirror: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "wrote pre-change mirror to %s\n", opts.mirror)

	result, err := service.ApplyFaceQualityRetirement(db, plan)
	if err != nil {
		fmt.Fprintf(stderr, "error: apply: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(stderr, "error: encode result: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "apply done idempotent=%v changed=%d marker=%s\n",
		result.Idempotent, len(result.Changed), result.MigrationKey)
	return 0
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	fs := flag.NewFlagSet("retire-face-quality", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "Path to SQLite database (required). Prefer a consistency-copied replica.")
	apply := fs.Bool("apply", false, "Write changes. Default is dry-run (plan only).")
	out := fs.String("out", "", "Optional path to write dry-run JSON plan (default stdout)")
	planPath := fs.String("plan", "", "Required with -apply: previously reviewed dry-run JSON plan")
	mirror := fs.String("mirror", "", "Required with -apply: write pre-change plan JSON mirror before writes")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	opts := options{
		dbPath:   strings.TrimSpace(*dbPath),
		apply:    *apply,
		out:      strings.TrimSpace(*out),
		mirror:   strings.TrimSpace(*mirror),
		planPath: strings.TrimSpace(*planPath),
	}
	if opts.dbPath == "" {
		fmt.Fprintln(stderr, "error: -db is required")
		printUsage(stderr)
		return options{}, errors.New("missing -db")
	}
	if opts.apply && opts.mirror == "" {
		fmt.Fprintln(stderr, "error: -mirror is required with -apply (pre-change plan mirror)")
		printUsage(stderr)
		return options{}, errors.New("missing -mirror")
	}
	if opts.apply && opts.planPath == "" {
		fmt.Fprintln(stderr, "error: -plan is required with -apply (reviewed dry-run plan)")
		return options{}, errors.New("missing -plan")
	}
	for _, output := range []string{opts.out, opts.mirror} {
		if output == "" {
			continue
		}
		for _, input := range []string{opts.dbPath, opts.planPath} {
			if input == "" {
				continue
			}
			a, _ := filepath.Abs(output)
			b, _ := filepath.Abs(input)
			if a == b {
				return options{}, errors.New("output must not overwrite database or approved plan")
			}
		}
	}
	return opts, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `
Usage:
  retire-face-quality -db /path/to/relive.db [-out plan.json]
  retire-face-quality -db /path/to/relive.db -apply -plan plan.json -mirror before.json

Default is dry-run (read-only). -apply requires -plan and -mirror and writes in a transaction
after fingerprint re-check. Does not call ML, does not full recluster. unknown
items are listed and never counted as cleaned.`)
}

func openDB(dbPath string, writable bool) (*gorm.DB, error) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resolve db path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("database file not found: %s", abs)
	}

	dsn := abs
	if !writable {
		// mode=ro 防止误写；query_only 双保险。
		dsn = fmt.Sprintf("file:%s?mode=ro&_query_only=true&_busy_timeout=60000", abs)
	} else {
		dsn = fmt.Sprintf("file:%s?_busy_timeout=60000&_journal_mode=WAL", abs)
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)

	if !writable {
		if _, err := sqlDB.Exec("PRAGMA query_only=ON"); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("set query_only: %w", err)
		}
	}
	return db, nil
}

func writePlan(outPath string, plan *service.RetirementPlan, stdout io.Writer) error {
	raw, err := service.MarshalRetirementPlanJSON(plan)
	if err != nil {
		return err
	}
	if outPath == "" {
		_, err := stdout.Write(append(raw, '\n'))
		return err
	}
	f, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(raw, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func readApprovedPlan(path string) (*service.RetirementPlan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var plan service.RetirementPlan
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return nil, err
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("unexpected trailing plan data")
	}
	if plan.GeneratedAt.IsZero() || plan.Summary == nil || plan.Summary["total"] != len(plan.Items) {
		return nil, errors.New("invalid reviewed plan")
	}
	return &plan, nil
}
