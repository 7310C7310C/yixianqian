// Command regionfix normalizes users.city / native_place / church_location to
// values from the vendored region dataset. It is a one-off maintenance tool for
// staging test data and the future production migration; it is dry-run by
// default and only writes with -apply.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/platform"
	"github.com/7310C7310C/yixianqian/internal/regions"
)

const fallbackCity = "北京"

var backupTablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "regionfix:", err)
		os.Exit(1)
	}
}

type fixResult struct {
	City           string
	NativePlace    string
	ChurchLocation string
	CityChanged    bool
	NativeChanged  bool
	ChurchChanged  bool
	CityFallback   bool
	NativeFallback bool
	ChurchFallback bool
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := platform.NewLogger(cfg.LogLevel, "text", os.Stderr)

	apply := flag.Bool("apply", false, "write repairs (default: dry-run)")
	verify := flag.Bool("verify", false, "only verify that every region value is valid")
	limit := flag.Int("limit", 0, "inspect at most N users (0 = all)")
	sample := flag.Int("sample", 15, "print at most N changed rows")
	backupTable := flag.String("backup-table", "", "create this users region backup table before -apply (required with -apply)")
	flag.Parse()

	if *verify && *limit > 0 {
		return errors.New("-verify always checks every row; remove -limit")
	}
	if *apply && *limit > 0 {
		return errors.New("-apply always repairs every row; remove -limit")
	}
	if *apply && !backupTablePattern.MatchString(*backupTable) {
		return errors.New("-apply requires -backup-table with a safe SQL identifier (letters, digits, underscore)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	rows, err := database.ListUserRegionRows(ctx, *limit)
	if err != nil {
		return err
	}

	if *verify {
		invalid := 0
		for _, row := range rows {
			if problems := invalidRegions(row); len(problems) > 0 {
				invalid++
				if invalid <= *sample {
					fmt.Printf("invalid id=%d legacy=%s problems=%v\n", row.ID, row.LegacyUserID, problems)
				}
			}
		}
		fmt.Printf("verify: rows=%d invalid=%d\n", len(rows), invalid)
		if invalid > 0 {
			return fmt.Errorf("%d rows still have invalid region values", invalid)
		}
		return nil
	}

	updates := make([]db.UserRegionUpdate, 0, len(rows))
	var cityChanged, nativeChanged, churchChanged int
	var cityFallback, nativeFallback, churchFallback int
	printed := 0
	for _, row := range rows {
		fix := repairRow(row.City, row.NativePlace, row.ChurchLocation)
		if !fix.CityChanged && !fix.NativeChanged && !fix.ChurchChanged {
			continue
		}
		updates = append(updates, db.UserRegionUpdate{
			ID:             row.ID,
			City:           fix.City,
			NativePlace:    fix.NativePlace,
			ChurchLocation: fix.ChurchLocation,
		})
		if fix.CityChanged {
			cityChanged++
		}
		if fix.NativeChanged {
			nativeChanged++
		}
		if fix.ChurchChanged {
			churchChanged++
		}
		if fix.CityFallback {
			cityFallback++
		}
		if fix.NativeFallback {
			nativeFallback++
		}
		if fix.ChurchFallback {
			churchFallback++
		}
		if printed < *sample {
			printed++
			fmt.Printf("id=%d legacy=%s city=%q->%q native=%q->%q church=%q->%q\n",
				row.ID, row.LegacyUserID, row.City, fix.City, row.NativePlace, fix.NativePlace,
				row.ChurchLocation, fix.ChurchLocation)
		}
	}

	fmt.Printf("summary: rows=%d changed=%d city_changed=%d city_fallback=%d native_changed=%d native_fallback=%d church_changed=%d church_fallback=%d\n",
		len(rows), len(updates), cityChanged, cityFallback, nativeChanged, nativeFallback, churchChanged, churchFallback)
	if !*apply {
		fmt.Println("dry-run: no rows written (pass -apply to write)")
		return nil
	}

	if err := database.BackupUserRegions(ctx, *backupTable); err != nil {
		return err
	}
	if err := database.UpdateUserRegions(ctx, updates); err != nil {
		return err
	}
	logger.Info("regionfix applied", "rows", len(updates))

	after, err := database.ListUserRegionRows(ctx, 0)
	if err != nil {
		return err
	}
	invalid := 0
	for _, row := range after {
		if problems := invalidRegions(row); len(problems) > 0 {
			invalid++
			if invalid <= *sample {
				fmt.Printf("still invalid id=%d legacy=%s problems=%v\n", row.ID, row.LegacyUserID, problems)
			}
		}
	}
	fmt.Printf("applied rows=%d invalid_after=%d\n", len(updates), invalid)
	if invalid > 0 {
		return fmt.Errorf("%d rows remain invalid after apply", invalid)
	}
	return nil
}

// repairRow keeps every value that already normalizes and falls back only for
// empty/invalid input. Hometown falls back to the repaired current city; the
// church location falls back to the city's first county-level division.
func repairRow(city, nativePlace, churchLocation string) fixResult {
	out := fixResult{
		City:           city,
		NativePlace:    nativePlace,
		ChurchLocation: churchLocation,
	}
	if fixed, ok := regions.NormalizeCityValue(city); ok {
		out.City = fixed
	} else {
		out.City = fallbackCity
		out.CityFallback = true
	}
	out.CityChanged = out.City != city

	if fixed, ok := regions.NormalizeCityValue(nativePlace); ok {
		out.NativePlace = fixed
	} else {
		out.NativePlace = out.City
		out.NativeFallback = true
	}
	out.NativeChanged = out.NativePlace != nativePlace

	if fixed, ok := regions.NormalizeAreaValue(churchLocation); ok {
		out.ChurchLocation = fixed
	} else {
		fixed, ok := regions.DefaultAreaForCity(out.City)
		if !ok {
			fixed = out.City
		}
		out.ChurchLocation = fixed
		out.ChurchFallback = true
	}
	out.ChurchChanged = out.ChurchLocation != churchLocation

	return out
}

func invalidRegions(row db.UserRegionRow) []string {
	var problems []string
	if _, ok := regions.NormalizeCityValue(row.City); !ok {
		problems = append(problems, "city")
	}
	if _, ok := regions.NormalizeCityValue(row.NativePlace); !ok {
		problems = append(problems, "native_place")
	}
	if _, ok := regions.NormalizeAreaValue(row.ChurchLocation); !ok {
		problems = append(problems, "church_location")
	}
	return problems
}
