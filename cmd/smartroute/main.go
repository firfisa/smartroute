package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/firfisa/smartroute/internal/config"
	"github.com/firfisa/smartroute/internal/decision"
	"github.com/firfisa/smartroute/internal/model"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("missing command")
	}

	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "smartroute %s commit=%s built=%s\n", version, commit, date)
		return nil
	case "validate":
		return runValidate(args[1:], stdout, stderr)
	case "trace":
		return runTrace(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runValidate(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := config.Load(*path); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "valid: %s\n", *path)
	return nil
}

func runTrace(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("trace", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "configs/smartroute.example.json", "path to SmartRoute JSON config")
	directSpec := flags.String("direct", "success:tls:35", "success:stage:latency_ms or failure:stage:latency_ms:class")
	proxySpec := flags.String("proxy", "success:tls:120", "success:stage:latency_ms or failure:stage:latency_ms:class")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	directObservation, err := parseObservation(model.PathDirect, *directSpec)
	if err != nil {
		return fmt.Errorf("direct: %w", err)
	}
	proxyObservation, err := parseObservation(model.PathProxy, *proxySpec)
	if err != nil {
		return fmt.Errorf("proxy: %w", err)
	}

	evaluator := decision.PairEvaluator{
		OriginalFallback: cfg.OriginalFallback,
		MaxDirectPenalty: time.Duration(cfg.Decision.MaxDirectPenaltyMS) * time.Millisecond,
	}
	result, err := evaluator.Evaluate(directObservation, proxyObservation)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func parseObservation(path model.Path, spec string) (model.Observation, error) {
	parts := strings.Split(spec, ":")
	if len(parts) < 3 || len(parts) > 4 {
		return model.Observation{}, errors.New("expected success:stage:latency_ms or failure:stage:latency_ms:class")
	}
	success := parts[0] == "success"
	if !success && parts[0] != "failure" {
		return model.Observation{}, errors.New("status must be success or failure")
	}
	stage, err := model.ParseStage(parts[1])
	if err != nil {
		return model.Observation{}, err
	}
	latencyMS, err := strconv.Atoi(parts[2])
	if err != nil || latencyMS < 0 {
		return model.Observation{}, errors.New("latency_ms must be a non-negative integer")
	}
	failureClass := ""
	if !success {
		if len(parts) != 4 || parts[3] == "" {
			return model.Observation{}, errors.New("failed observation requires a failure class")
		}
		failureClass = parts[3]
	} else if len(parts) == 4 {
		return model.Observation{}, errors.New("successful observation must not include a failure class")
	}

	observation := model.Observation{
		Path: path, Success: success, StageReached: stage,
		Latency: time.Duration(latencyMS) * time.Millisecond, FailureClass: failureClass,
	}
	if err := observation.Validate(); err != nil {
		return model.Observation{}, err
	}
	return observation, nil
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `SmartRoute experimental CLI

Usage:
  smartroute version
  smartroute validate [-config path]
  smartroute trace [-config path] [-direct spec] [-proxy spec]

The trace command evaluates one synthetic paired observation. It does not
open network connections or persist learned policy.`)
}
