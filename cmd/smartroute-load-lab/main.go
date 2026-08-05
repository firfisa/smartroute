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
	defaults := loadlab.DefaultOptions()
	runs := flag.Int("runs", defaults.Runs, "number of separately reported load runs")
	concurrency := flag.Int("concurrency", defaults.Concurrency, "concurrent fresh connections per measured arm")
	bytesPerConnection := flag.Int64("bytes-per-connection", defaults.BytesPerConnection, "verified one-way payload bytes per measured connection")
	chunkBytes := flag.Int("chunk-bytes", defaults.ChunkBytes, "write/read echo chunk size")
	warmupConnections := flag.Int("warmup-connections", defaults.WarmupConnections, "unreported warmup connections per arm and run")
	minRatio := flag.Float64("min-throughput-ratio", defaults.MinThroughputRatio, "minimum sidecar/baseline payload throughput ratio")
	enforce := flag.Bool("enforce", false, "exit non-zero when the environment-dependent throughput gate is missed")
	mihomo := flag.String("mihomo", "", "explicit pinned Mihomo executable for the isolated forced-DIRECT tier")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	report, err := loadlab.Run(ctx, loadlab.Options{
		Runs: *runs, Concurrency: *concurrency, BytesPerConnection: *bytesPerConnection, ChunkBytes: *chunkBytes,
		WarmupConnections: *warmupConnections, MinThroughputRatio: *minRatio, EnforceThroughputGate: *enforce, MihomoPath: *mihomo,
	})
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
