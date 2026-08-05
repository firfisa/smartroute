package loadlab

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const CurrentSweepReportVersion = 1

type SweepCell struct {
	Concurrency        int   `json:"concurrency"`
	BytesPerConnection int64 `json:"bytes_per_connection"`
}

type SweepOptions struct {
	Runs               int
	ChunkBytes         int
	WarmupMax          int
	MinThroughputRatio float64
	MihomoPath         string
	Cells              []SweepCell
}

func DefaultSweepOptions() SweepOptions {
	return SweepOptions{
		Runs: 3, ChunkBytes: 32 << 10, WarmupMax: 4, MinThroughputRatio: 0.70,
		Cells: []SweepCell{
			{Concurrency: 1, BytesPerConnection: 1 << 20},
			{Concurrency: 4, BytesPerConnection: 1 << 20},
			{Concurrency: 16, BytesPerConnection: 64 << 10},
			{Concurrency: 16, BytesPerConnection: 1 << 20},
			{Concurrency: 16, BytesPerConnection: 8 << 20},
			{Concurrency: 64, BytesPerConnection: 1 << 20},
		},
	}
}

func (o SweepOptions) Validate() error {
	if len(o.Cells) == 0 || len(o.Cells) > 32 {
		return errors.New("load sweep must contain between 1 and 32 cells")
	}
	if o.Runs < 1 || o.Runs > 10 {
		return errors.New("load sweep runs must be between 1 and 10")
	}
	if o.WarmupMax < 0 || o.WarmupMax > 512 {
		return errors.New("load sweep warmup max must be between 0 and 512")
	}
	seen := make(map[SweepCell]struct{}, len(o.Cells))
	for index, cell := range o.Cells {
		if _, duplicate := seen[cell]; duplicate {
			return fmt.Errorf("load sweep cell %d duplicates an earlier cell", index)
		}
		seen[cell] = struct{}{}
		options := Options{
			Runs: o.Runs, Concurrency: cell.Concurrency, BytesPerConnection: cell.BytesPerConnection,
			ChunkBytes: o.ChunkBytes, WarmupConnections: min(o.WarmupMax, cell.Concurrency), MinThroughputRatio: o.MinThroughputRatio,
		}
		if err := options.Validate(); err != nil {
			return fmt.Errorf("load sweep cell %d: %w", index, err)
		}
	}
	return nil
}

type SweepReport struct {
	ReportVersion              int                `json:"report_version"`
	GeneratedAt                time.Time          `json:"generated_at"`
	Tier                       string             `json:"tier"`
	Summaries                  []SweepCellSummary `json:"summaries"`
	Cells                      []SweepCellResult  `json:"cells"`
	AllCorrect                 bool               `json:"all_correct"`
	AllMeetThroughputGate      bool               `json:"all_meet_throughput_gate"`
	AllRuntimeMetricsAvailable bool               `json:"all_runtime_metrics_available"`
	PerformanceGateEnforced    bool               `json:"performance_gate_enforced"`
	AuthorizesLiveTrial        bool               `json:"authorizes_live_trial"`
	AuthorizesPolicyChange     bool               `json:"authorizes_policy_change"`
	Passed                     bool               `json:"passed"`
}

type SweepCellSummary struct {
	Cell                             SweepCell `json:"cell"`
	BaselinePayloadMiBPerSecondP50   float64   `json:"baseline_payload_mib_per_second_p50"`
	SidecarPayloadMiBPerSecondP50    float64   `json:"sidecar_payload_mib_per_second_p50"`
	ThroughputRatioP50               float64   `json:"throughput_ratio_p50"`
	WorstRunThroughputRatio          float64   `json:"worst_run_throughput_ratio"`
	BaselineAllocatedBytesTotal      uint64    `json:"baseline_allocated_bytes_total"`
	SidecarAllocatedBytesTotal       uint64    `json:"sidecar_allocated_bytes_total"`
	AllocatedBytesRatio              float64   `json:"allocated_bytes_ratio"`
	BaselineAllocationObjectsTotal   uint64    `json:"baseline_allocation_objects_total"`
	SidecarAllocationObjectsTotal    uint64    `json:"sidecar_allocation_objects_total"`
	BaselineUserCPUSecondsTotal      float64   `json:"baseline_user_cpu_seconds_total"`
	SidecarUserCPUSecondsTotal       float64   `json:"sidecar_user_cpu_seconds_total"`
	UserCPUShortWindowDiagnosticOnly bool      `json:"user_cpu_short_window_diagnostic_only"`
	MeetsThroughputGate              bool      `json:"meets_throughput_gate"`
}

type SweepCellResult struct {
	Cell   SweepCell `json:"cell"`
	Report Report    `json:"report"`
}

func RunSweep(ctx context.Context, options SweepOptions) (SweepReport, error) {
	report := SweepReport{
		ReportVersion: CurrentSweepReportVersion, GeneratedAt: time.Now().UTC(), Tier: "fake_socks_gateway",
		AllCorrect: true, AllMeetThroughputGate: true, AllRuntimeMetricsAvailable: true, PerformanceGateEnforced: false,
		AuthorizesLiveTrial: false, AuthorizesPolicyChange: false,
	}
	if options.MihomoPath != "" {
		report.Tier = "pinned_mihomo_forced_direct"
	}
	if err := options.Validate(); err != nil {
		report.AllCorrect = false
		report.AllRuntimeMetricsAvailable = false
		return report, err
	}
	for _, cell := range options.Cells {
		loadOptions := Options{
			Runs: options.Runs, Concurrency: cell.Concurrency, BytesPerConnection: cell.BytesPerConnection,
			ChunkBytes: options.ChunkBytes, WarmupConnections: min(options.WarmupMax, cell.Concurrency),
			MinThroughputRatio: options.MinThroughputRatio, MihomoPath: options.MihomoPath,
		}
		cellReport, err := Run(ctx, loadOptions)
		summary := summarizeSweepCell(cell, cellReport)
		report.Summaries = append(report.Summaries, summary)
		report.Cells = append(report.Cells, SweepCellResult{Cell: cell, Report: cellReport})
		report.AllCorrect = report.AllCorrect && cellReport.Correctness.Passed
		report.AllMeetThroughputGate = report.AllMeetThroughputGate && cellReport.MeetsThroughputGate
		for _, run := range cellReport.Runs {
			report.AllRuntimeMetricsAvailable = report.AllRuntimeMetricsAvailable && run.Baseline.Runtime.Available && run.Sidecar.Runtime.Available
		}
		if err != nil {
			report.Passed = false
			return report, fmt.Errorf("load sweep concurrency=%d bytes=%d: %w", cell.Concurrency, cell.BytesPerConnection, err)
		}
	}
	report.Passed = report.AllCorrect
	return report, nil
}

func summarizeSweepCell(cell SweepCell, report Report) SweepCellSummary {
	summary := SweepCellSummary{
		Cell: cell, BaselinePayloadMiBPerSecondP50: report.Aggregate.BaselinePayloadMiBPerSecond.P50,
		SidecarPayloadMiBPerSecondP50: report.Aggregate.SidecarPayloadMiBPerSecond.P50,
		ThroughputRatioP50:            report.Aggregate.ThroughputRatio.P50, WorstRunThroughputRatio: report.WorstRunThroughputRatio,
		UserCPUShortWindowDiagnosticOnly: true, MeetsThroughputGate: report.MeetsThroughputGate,
	}
	for _, run := range report.Runs {
		summary.BaselineAllocatedBytesTotal += run.Baseline.Runtime.AllocatedBytes
		summary.SidecarAllocatedBytesTotal += run.Sidecar.Runtime.AllocatedBytes
		summary.BaselineAllocationObjectsTotal += run.Baseline.Runtime.AllocationObjects
		summary.SidecarAllocationObjectsTotal += run.Sidecar.Runtime.AllocationObjects
		summary.BaselineUserCPUSecondsTotal += run.Baseline.Runtime.UserCPUSeconds
		summary.SidecarUserCPUSecondsTotal += run.Sidecar.Runtime.UserCPUSeconds
	}
	if summary.BaselineAllocatedBytesTotal > 0 {
		summary.AllocatedBytesRatio = float64(summary.SidecarAllocatedBytesTotal) / float64(summary.BaselineAllocatedBytesTotal)
	}
	return summary
}
