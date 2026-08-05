package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/firfisa/smartroute/internal/mihomolab"
)

func main() {
	mihomoPath := flag.String("mihomo", "", "explicit path to the pinned Mihomo v1.19.29 test binary")
	smartRoutePath := flag.String("smartroute", "", "explicit path to the SmartRoute binary under test")
	nodePath := flag.String("node", "node", "Node.js executable used to run the real Clash transform")
	composerPath := flag.String("composer", "", "explicit path to compose-clash-script.mjs")
	applyPath := flag.String("apply-script", "", "explicit path to apply-composed-clash-script.mjs")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	report, err := mihomolab.RunRuntime(ctx, mihomolab.RuntimeOptions{
		MihomoBinary: *mihomoPath, SmartRouteBinary: *smartRoutePath, NodeBinary: *nodePath,
		ComposerScript: *composerPath, ApplyScript: *applyPath,
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
