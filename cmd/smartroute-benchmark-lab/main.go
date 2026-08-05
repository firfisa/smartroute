package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/firfisa/smartroute/internal/benchlab"
)

func main() {
	defaults := benchlab.DefaultOptions()
	runs := flag.Int("runs", defaults.Runs, "number of separately reported benchmark runs")
	samples := flag.Int("samples", defaults.Samples, "number of alternating baseline/sidecar sample pairs")
	warmup := flag.Int("warmup", defaults.Warmup, "number of unreported warmup pairs")
	maxP95 := flag.Duration("max-p95-overhead", defaults.MaxP95Overhead, "paired p95 overhead gate")
	enforce := flag.Bool("enforce", false, "exit non-zero when the environment-dependent p95 gate is exceeded")
	mihomo := flag.String("mihomo", "", "explicit pinned Mihomo executable for the isolated forced-DIRECT tier")
	tlsReady := flag.Bool("tls", false, "measure through a validated ClientHello-to-ServerHello exchange")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	report, err := benchlab.Run(ctx, benchlab.Options{
		Runs: *runs, Samples: *samples, Warmup: *warmup, MaxP95Overhead: *maxP95, EnforceLatencyGate: *enforce, MihomoPath: *mihomo, TLS: *tlsReady,
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
