package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/firfisa/smartroute/internal/loadlab"
)

func main() {
	defaults := loadlab.DefaultCapacityOptions()
	runs := flag.Int("runs", defaults.Runs, "runs per fixed offered-load cell")
	concurrency := flag.Int("concurrency", defaults.Concurrency, "concurrent measured connections per arm")
	bytesPerConnection := flag.Int64("bytes-per-connection", defaults.BytesPerConnection, "verified one-way bytes per connection")
	chunkBytes := flag.Int("chunk-bytes", defaults.ChunkBytes, "paced write/read echo chunk size")
	warmupConnections := flag.Int("warmup-connections", defaults.WarmupConnections, "unpaced warmup connections per arm and cell")
	tolerance := flag.Float64("deadline-tolerance-ratio", defaults.DeadlineToleranceRatio, "report-only relative completion allowance")
	minimumGrace := flag.Duration("minimum-deadline-grace", defaults.MinimumDeadlineGrace, "report-only absolute completion allowance")
	mihomo := flag.String("mihomo", "", "explicit pinned Mihomo executable for every isolated cell")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	options := defaults
	options.Runs, options.Concurrency = *runs, *concurrency
	options.BytesPerConnection, options.ChunkBytes = *bytesPerConnection, *chunkBytes
	options.WarmupConnections = *warmupConnections
	options.DeadlineToleranceRatio, options.MinimumDeadlineGrace = *tolerance, *minimumGrace
	options.MihomoPath = *mihomo
	report, err := loadlab.RunCapacity(ctx, options)
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if encodeErr := encoder.Encode(report); encodeErr != nil {
		fmt.Fprintln(os.Stderr, "error: encode report:", encodeErr)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
