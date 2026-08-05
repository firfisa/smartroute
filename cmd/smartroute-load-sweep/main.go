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
	defaults := loadlab.DefaultSweepOptions()
	runs := flag.Int("runs", defaults.Runs, "runs per fixed concurrency/payload sweep cell")
	chunkBytes := flag.Int("chunk-bytes", defaults.ChunkBytes, "chunk size shared by every cell")
	warmupMax := flag.Int("warmup-max", defaults.WarmupMax, "maximum warmup concurrency per arm and cell")
	minRatio := flag.Float64("min-throughput-ratio", defaults.MinThroughputRatio, "report-only minimum per-cell worst-run ratio")
	mihomo := flag.String("mihomo", "", "explicit pinned Mihomo executable for every isolated cell")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	options := defaults
	options.Runs, options.ChunkBytes, options.WarmupMax = *runs, *chunkBytes, *warmupMax
	options.MinThroughputRatio, options.MihomoPath = *minRatio, *mihomo
	report, err := loadlab.RunSweep(ctx, options)
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
