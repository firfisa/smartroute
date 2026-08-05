package loadlab

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const CurrentCapacityReportVersion = 1

type CapacityOptions struct {
	Runs                     int
	Concurrency              int
	BytesPerConnection       int64
	ChunkBytes               int
	WarmupConnections        int
	DeadlineToleranceRatio   float64
	MinimumDeadlineGrace     time.Duration
	MihomoPath               string
	AggregateOfferedLoadMbps []float64
}

func DefaultCapacityOptions() CapacityOptions {
	return CapacityOptions{
		Runs: 2, Concurrency: 16, BytesPerConnection: 1 << 20, ChunkBytes: 32 << 10,
		WarmupConnections: 4, DeadlineToleranceRatio: 0.03, MinimumDeadlineGrace: time.Millisecond,
		AggregateOfferedLoadMbps: []float64{100, 500, 1000, 5000, 8000},
	}
}

func (o CapacityOptions) Validate() error {
	if len(o.AggregateOfferedLoadMbps) == 0 || len(o.AggregateOfferedLoadMbps) > 16 {
		return errors.New("capacity lab must contain between 1 and 16 offered-load cells")
	}
	if o.DeadlineToleranceRatio < 0 || o.DeadlineToleranceRatio > 0.25 {
		return errors.New("capacity deadline tolerance ratio must be between zero and 0.25")
	}
	if o.MinimumDeadlineGrace < 0 || o.MinimumDeadlineGrace > time.Second {
		return errors.New("capacity minimum deadline grace must be between zero and one second")
	}
	seen := make(map[float64]struct{}, len(o.AggregateOfferedLoadMbps))
	for index, offeredLoad := range o.AggregateOfferedLoadMbps {
		if offeredLoad <= 0 {
			return fmt.Errorf("capacity cell %d offered load must be positive", index)
		}
		if _, duplicate := seen[offeredLoad]; duplicate {
			return fmt.Errorf("capacity cell %d duplicates an earlier offered load", index)
		}
		seen[offeredLoad] = struct{}{}
		loadOptions := Options{
			Runs: o.Runs, Concurrency: o.Concurrency, BytesPerConnection: o.BytesPerConnection,
			ChunkBytes: o.ChunkBytes, WarmupConnections: o.WarmupConnections,
			MinThroughputRatio: 0.70, MihomoPath: o.MihomoPath, AggregateOfferedLoadMbps: offeredLoad,
		}
		if err := loadOptions.Validate(); err != nil {
			return fmt.Errorf("capacity cell %d: %w", index, err)
		}
	}
	return nil
}

type CapacityReport struct {
	ReportVersion           int                   `json:"report_version"`
	GeneratedAt             time.Time             `json:"generated_at"`
	Tier                    string                `json:"tier"`
	Measurement             CapacityMeasurement   `json:"measurement"`
	Summaries               []CapacityCellSummary `json:"summaries"`
	Cells                   []CapacityCellResult  `json:"cells"`
	AllCorrect              bool                  `json:"all_correct"`
	AllBaselineCellsMeet    bool                  `json:"all_baseline_cells_meet"`
	AllSidecarCellsMeet     bool                  `json:"all_sidecar_cells_meet"`
	AllCellsComparable      bool                  `json:"all_cells_comparable"`
	PerformanceGateEnforced bool                  `json:"performance_gate_enforced"`
	AuthorizesLiveTrial     bool                  `json:"authorizes_live_trial"`
	AuthorizesPolicyChange  bool                  `json:"authorizes_policy_change"`
	Passed                  bool                  `json:"passed"`
}

type CapacityMeasurement struct {
	OfferedLoadDefinition       string  `json:"offered_load_definition"`
	PacingLocation              string  `json:"pacing_location"`
	DeadlineToleranceRatio      float64 `json:"deadline_tolerance_ratio"`
	MinimumDeadlineGraceUS      int64   `json:"minimum_deadline_grace_us"`
	RepresentsNetworkEmulation  bool    `json:"represents_network_emulation"`
	RepresentsRTTOrLoss         bool    `json:"represents_rtt_or_loss"`
	RepresentsApplicationDemand bool    `json:"represents_application_demand"`
}

type CapacityCellSummary struct {
	AggregateOfferedLoadMbps float64 `json:"aggregate_offered_load_mbps"`
	TargetDurationUS         int64   `json:"target_duration_us"`
	DeadlineAllowanceUS      int64   `json:"deadline_allowance_us"`
	BaselineWorstOverrunUS   int64   `json:"baseline_worst_overrun_us"`
	SidecarWorstOverrunUS    int64   `json:"sidecar_worst_overrun_us"`
	BaselineMeetsOfferedLoad bool    `json:"baseline_meets_offered_load"`
	SidecarMeetsOfferedLoad  bool    `json:"sidecar_meets_offered_load"`
	Comparable               bool    `json:"comparable"`
	Correct                  bool    `json:"correct"`
}

type CapacityCellResult struct {
	AggregateOfferedLoadMbps float64 `json:"aggregate_offered_load_mbps"`
	Report                   Report  `json:"report"`
}

func RunCapacity(ctx context.Context, options CapacityOptions) (CapacityReport, error) {
	report := CapacityReport{
		ReportVersion: CurrentCapacityReportVersion, GeneratedAt: time.Now().UTC(), Tier: "fake_socks_gateway",
		Measurement: CapacityMeasurement{
			OfferedLoadDefinition:      "aggregate_verified_one_way_payload_megabits_per_second",
			PacingLocation:             "measured_clients_absolute_cumulative_byte_schedule",
			DeadlineToleranceRatio:     options.DeadlineToleranceRatio,
			MinimumDeadlineGraceUS:     options.MinimumDeadlineGrace.Microseconds(),
			RepresentsNetworkEmulation: false, RepresentsRTTOrLoss: false, RepresentsApplicationDemand: true,
		},
		AllCorrect: true, AllBaselineCellsMeet: true, AllSidecarCellsMeet: true, AllCellsComparable: true,
		PerformanceGateEnforced: false, AuthorizesLiveTrial: false, AuthorizesPolicyChange: false,
	}
	if options.MihomoPath != "" {
		report.Tier = "pinned_mihomo_forced_direct"
	}
	if err := options.Validate(); err != nil {
		report.AllCorrect = false
		return report, err
	}
	for _, offeredLoad := range options.AggregateOfferedLoadMbps {
		loadOptions := Options{
			Runs: options.Runs, Concurrency: options.Concurrency, BytesPerConnection: options.BytesPerConnection,
			ChunkBytes: options.ChunkBytes, WarmupConnections: options.WarmupConnections,
			MinThroughputRatio: 0.70, MihomoPath: options.MihomoPath, AggregateOfferedLoadMbps: offeredLoad,
		}
		cellReport, err := Run(ctx, loadOptions)
		summary := summarizeCapacityCell(offeredLoad, cellReport, options.DeadlineToleranceRatio, options.MinimumDeadlineGrace)
		report.Summaries = append(report.Summaries, summary)
		report.Cells = append(report.Cells, CapacityCellResult{AggregateOfferedLoadMbps: offeredLoad, Report: cellReport})
		report.AllCorrect = report.AllCorrect && summary.Correct
		report.AllBaselineCellsMeet = report.AllBaselineCellsMeet && summary.BaselineMeetsOfferedLoad
		report.AllSidecarCellsMeet = report.AllSidecarCellsMeet && summary.SidecarMeetsOfferedLoad
		report.AllCellsComparable = report.AllCellsComparable && summary.Comparable
		if err != nil {
			report.Passed = false
			return report, fmt.Errorf("capacity cell offered_load_mbps=%g: %w", offeredLoad, err)
		}
	}
	report.Passed = report.AllCorrect
	return report, nil
}

func summarizeCapacityCell(offeredLoad float64, report Report, toleranceRatio float64, minimumGrace time.Duration) CapacityCellSummary {
	summary := CapacityCellSummary{AggregateOfferedLoadMbps: offeredLoad, Correct: report.Correctness.Passed}
	for _, run := range report.Runs {
		if summary.TargetDurationUS == 0 {
			summary.TargetDurationUS = run.Baseline.Pacing.TargetDurationUS
		}
		if run.Baseline.Pacing.DeadlineOverrunUS > summary.BaselineWorstOverrunUS {
			summary.BaselineWorstOverrunUS = run.Baseline.Pacing.DeadlineOverrunUS
		}
		if run.Sidecar.Pacing.DeadlineOverrunUS > summary.SidecarWorstOverrunUS {
			summary.SidecarWorstOverrunUS = run.Sidecar.Pacing.DeadlineOverrunUS
		}
	}
	allowance := int64(float64(summary.TargetDurationUS) * toleranceRatio)
	if minimumGrace.Microseconds() > allowance {
		allowance = minimumGrace.Microseconds()
	}
	summary.DeadlineAllowanceUS = allowance
	summary.BaselineMeetsOfferedLoad = summary.Correct && summary.BaselineWorstOverrunUS <= allowance
	summary.SidecarMeetsOfferedLoad = summary.Correct && summary.SidecarWorstOverrunUS <= allowance
	summary.Comparable = summary.BaselineMeetsOfferedLoad
	return summary
}
