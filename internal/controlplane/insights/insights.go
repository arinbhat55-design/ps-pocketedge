// Package insights turns a container's recorded resource-usage history
// (container_metric_samples) into three things the raw charts don't say on
// their own: abnormal-usage detection, threshold-rule evaluation for
// alerts, and resource-allocation recommendations.
//
// Everything here is pure computation over []store.ContainerResourceUsage —
// no I/O — so the REST handler (on-demand insights for one container) and
// the background Evaluator (fleet-wide alerting) share one implementation,
// and it's unit-testable without Postgres.
package insights

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

// Metric names, shared by anomalies, alert rules, and recommendations.
const (
	MetricCPU    = "cpu"    // CPUPercent: % of one host core, so can exceed 100
	MetricMemory = "memory" // MemPercent: usage as % of the effective limit
	MetricPIDs   = "pids"   // process count
)

// ValidMetric reports whether m is one of the metrics above.
func ValidMetric(m string) bool {
	return m == MetricCPU || m == MetricMemory || m == MetricPIDs
}

// Severities, ordered least to most urgent.
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// metricValue extracts metric's value from one sample.
func metricValue(s store.ContainerResourceUsage, metric string) float64 {
	switch metric {
	case MetricCPU:
		return s.CPUPercent
	case MetricMemory:
		return s.MemPercent
	case MetricPIDs:
		return float64(s.PIDs)
	}
	return 0
}

// FormatValue renders a metric value for alert/anomaly messages. CPU is
// shown in cores (CPUPercent 150 → "1.50 cores"), the unit limits are set
// in, rather than as a percentage that can confusingly exceed 100.
func FormatValue(metric string, v float64) string {
	switch metric {
	case MetricPIDs:
		return fmt.Sprintf("%.0f processes", v)
	case MetricCPU:
		return fmt.Sprintf("%.2f cores", v/100)
	}
	return fmt.Sprintf("%.1f%%", v)
}

// MetricLabel is metric's human name for messages.
func MetricLabel(metric string) string {
	switch metric {
	case MetricCPU:
		return "CPU"
	case MetricMemory:
		return "Memory"
	case MetricPIDs:
		return "Process count"
	}
	return metric
}

// ---- Abnormal usage detection ----

const (
	// anomalyRecentWindow is the "now" slice compared against the baseline
	// before it: long enough to smooth over one noisy sample (heartbeats
	// are ~20s apart), short enough to flag a spike within minutes.
	anomalyRecentWindow = 5 * time.Minute
	// minBaselineSamples is how much history is needed before a baseline
	// is trusted — ~10 minutes at the agent's heartbeat rate. Less than
	// that and every container would look "abnormal" right after it starts.
	minBaselineSamples = 30
	// spikeZScore is how many baseline standard deviations above the
	// baseline mean the recent mean must sit to count as a spike.
	spikeZScore = 3.0
	// criticalZScore escalates a spike from warning to critical.
	criticalZScore = 6.0
	// leakMinSpan is the minimum history for memory-leak (steady growth)
	// detection; a short upward run is just warm-up, not a leak.
	leakMinSpan = 1 * time.Hour
	// leakMinR2 is how linear the growth must be (coefficient of
	// determination) — a leak climbs steadily, a busy cache sawtooths.
	leakMinR2 = 0.8
	// leakHorizon: only report a leak projected to hit the limit this soon.
	leakHorizon = 24 * time.Hour
)

// spikeFloor is the per-metric minimum absolute increase over the baseline
// mean for a spike, and the minimum standard deviation used for the
// z-score — without these, a container idling at a flat 0.1% CPU would
// flag a jump to 0.5% as a 40-sigma event.
var spikeFloor = map[string]struct{ minDelta, minStdDev float64 }{
	MetricCPU:    {minDelta: 15, minStdDev: 2},
	MetricMemory: {minDelta: 10, minStdDev: 1},
	MetricPIDs:   {minDelta: 10, minStdDev: 1},
}

// Anomaly is one detected abnormal-usage pattern.
type Anomaly struct {
	Metric   string  `json:"metric"`
	Kind     string  `json:"kind"` // "spike" or "leak"
	Severity string  `json:"severity"`
	Current  float64 `json:"current"`
	Baseline float64 `json:"baseline"`
	Message  string  `json:"message"`
}

// DetectAnomalies looks for abnormal usage in samples (oldest first, as
// ListContainerMetricSamples returns them), as of now:
//
//   - spike: the mean of the last anomalyRecentWindow sits spikeZScore+
//     standard deviations above the mean of everything before it (and by
//     a meaningful absolute margin — see spikeFloor). Checked for CPU,
//     memory, and process count.
//   - leak: memory usage grows steadily (a near-linear fit over at least
//     leakMinSpan) and is projected to reach the memory limit within
//     leakHorizon.
//
// Returns nil when there isn't enough history to judge.
func DetectAnomalies(samples []store.ContainerResourceUsage, now time.Time) []Anomaly {
	var anomalies []Anomaly
	cut := now.Add(-anomalyRecentWindow)
	split := len(samples)
	for i, s := range samples {
		if !s.RecordedAt.Before(cut) {
			split = i
			break
		}
	}
	baseline, recent := samples[:split], samples[split:]
	if len(baseline) >= minBaselineSamples && len(recent) > 0 {
		for _, metric := range []string{MetricCPU, MetricMemory, MetricPIDs} {
			if a, ok := detectSpike(metric, baseline, recent); ok {
				anomalies = append(anomalies, a)
			}
		}
	}
	if a, ok := detectLeak(samples); ok {
		anomalies = append(anomalies, a)
	}
	return anomalies
}

func detectSpike(metric string, baseline, recent []store.ContainerResourceUsage) (Anomaly, bool) {
	base := values(baseline, metric)
	mean, sd := meanStdDev(base)
	cur := mean64(values(recent, metric))
	floor := spikeFloor[metric]
	sd = max(sd, floor.minStdDev)
	delta := cur - mean
	// Process count scales with the workload, so its absolute floor also
	// grows with the baseline (doubling from 200 to 400 matters; 2 to 12
	// is covered by the fixed minDelta).
	minDelta := floor.minDelta
	if metric == MetricPIDs {
		minDelta = max(minDelta, mean*0.5)
	}
	z := delta / sd
	if z < spikeZScore || delta < minDelta {
		return Anomaly{}, false
	}
	severity := SeverityWarning
	if z >= criticalZScore {
		severity = SeverityCritical
	}
	return Anomaly{
		Metric:   metric,
		Kind:     "spike",
		Severity: severity,
		Current:  cur,
		Baseline: mean,
		Message: fmt.Sprintf("%s usage jumped to %s over the last %d minutes, versus a usual %s.",
			MetricLabel(metric), FormatValue(metric, cur), int(anomalyRecentWindow.Minutes()), FormatValue(metric, mean)),
	}, true
}

func detectLeak(samples []store.ContainerResourceUsage) (Anomaly, bool) {
	if len(samples) < minBaselineSamples {
		return Anomaly{}, false
	}
	first, last := samples[0], samples[len(samples)-1]
	span := last.RecordedAt.Sub(first.RecordedAt)
	if span < leakMinSpan || last.MemLimitBytes <= 0 {
		return Anomaly{}, false
	}
	xs := make([]float64, len(samples))
	ys := make([]float64, len(samples))
	for i, s := range samples {
		xs[i] = s.RecordedAt.Sub(first.RecordedAt).Seconds()
		ys[i] = float64(s.MemUsageBytes)
	}
	slope, r2 := linearFit(xs, ys) // bytes per second
	if slope <= 0 || r2 < leakMinR2 {
		return Anomaly{}, false
	}
	// Ignore slow drift: growth must amount to at least 10% of the limit
	// over the observed span to be worth an operator's attention.
	if slope*span.Seconds() < 0.1*float64(last.MemLimitBytes) {
		return Anomaly{}, false
	}
	remaining := float64(last.MemLimitBytes - last.MemUsageBytes)
	eta := time.Duration(remaining/slope) * time.Second
	if eta > leakHorizon {
		return Anomaly{}, false
	}
	severity := SeverityWarning
	if eta < 2*time.Hour {
		severity = SeverityCritical
	}
	return Anomaly{
		Metric:   MetricMemory,
		Kind:     "leak",
		Severity: severity,
		Current:  last.MemPercent,
		Baseline: first.MemPercent,
		Message: fmt.Sprintf("Memory has grown steadily from %.1f%% to %.1f%% over %s — possible leak; at this rate it reaches the limit in about %s.",
			first.MemPercent, last.MemPercent, humanDuration(span), humanDuration(eta)),
	}, true
}

// ---- Threshold rule evaluation ----

const (
	// staleAfter: if a container's newest sample is older than this, its
	// agent has stopped reporting and a rule can't be judged either way.
	staleAfter = 2 * time.Minute
)

// ThresholdResult is the outcome of checking one rule against one
// container's samples.
type ThresholdResult struct {
	Breaching bool
	// Value is the most recent value of the rule's metric.
	Value float64
}

// EvaluateThreshold reports whether metric has stayed above threshold for
// the whole of the last duration (a duration of 0 means "the latest sample
// is above threshold"). Requiring the breach to be sustained is what keeps
// a single CPU blip during a request burst from paging anyone. The window
// only counts as covered once samples reach back at least half of
// duration, so a container that just started can't breach a 10-minute
// rule off one sample.
func EvaluateThreshold(samples []store.ContainerResourceUsage, metric string, threshold float64, duration time.Duration, now time.Time) ThresholdResult {
	if len(samples) == 0 {
		return ThresholdResult{}
	}
	last := samples[len(samples)-1]
	res := ThresholdResult{Value: metricValue(last, metric)}
	if now.Sub(last.RecordedAt) > staleAfter {
		return res
	}
	if duration <= 0 {
		res.Breaching = res.Value > threshold
		return res
	}
	windowStart := now.Add(-duration)
	var earliest time.Time
	for i := len(samples) - 1; i >= 0; i-- {
		s := samples[i]
		if s.RecordedAt.Before(windowStart) {
			break
		}
		if metricValue(s, metric) <= threshold {
			return res
		}
		earliest = s.RecordedAt
	}
	res.Breaching = !earliest.IsZero() && now.Sub(earliest) >= duration/2
	return res
}

// ---- Resource allocation recommendations ----

const (
	nanoCPUsPerCore = 1_000_000_000
	mib             = 1024 * 1024
	// minRecommendationSamples: roughly 30 minutes of history. Sizing
	// limits off a few minutes of usage would just encode whatever the
	// container happened to be doing right then.
	minRecommendationSamples = 90
)

// Limits are a container's currently configured resource limits, as
// ContainerInspect reports them; zero means unlimited/unset.
type Limits struct {
	NanoCPUs               int64 `json:"nanoCpus"`
	MemoryLimitBytes       int64 `json:"memoryLimitBytes"`
	MemoryReservationBytes int64 `json:"memoryReservationBytes"`
	PidsLimit              int64 `json:"pidsLimit"`
}

// Recommendation is one suggested change to a container's limits.
type Recommendation struct {
	Resource  string `json:"resource"` // "cpu", "memory", "memoryReservation", "pids"
	Action    string `json:"action"`   // "set", "increase", or "decrease"
	Severity  string `json:"severity"`
	Current   int64  `json:"current"`   // same unit as Limits' field; 0 = unlimited
	Suggested int64  `json:"suggested"` // same unit
	Message   string `json:"message"`
}

// UsageStats summarizes the history recommendations were based on.
type UsageStats struct {
	SampleCount  int       `json:"sampleCount"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	CPUP50       float64   `json:"cpuP50"`
	CPUP95       float64   `json:"cpuP95"`
	CPUMax       float64   `json:"cpuMax"`
	MemBytesP50  int64     `json:"memBytesP50"`
	MemBytesP95  int64     `json:"memBytesP95"`
	MemBytesMax  int64     `json:"memBytesMax"`
	PIDsMax      int64     `json:"pidsMax"`
	HostMemLimit int64     `json:"hostMemLimit"`
}

// Summarize computes UsageStats over samples; ok is false for an empty
// slice.
func Summarize(samples []store.ContainerResourceUsage) (UsageStats, bool) {
	if len(samples) == 0 {
		return UsageStats{}, false
	}
	cpu := values(samples, MetricCPU)
	mem := make([]float64, len(samples))
	var pidsMax int64
	for i, s := range samples {
		mem[i] = float64(s.MemUsageBytes)
		pidsMax = max(pidsMax, s.PIDs)
	}
	return UsageStats{
		SampleCount:  len(samples),
		From:         samples[0].RecordedAt,
		To:           samples[len(samples)-1].RecordedAt,
		CPUP50:       percentile(cpu, 50),
		CPUP95:       percentile(cpu, 95),
		CPUMax:       percentile(cpu, 100),
		MemBytesP50:  int64(percentile(mem, 50)),
		MemBytesP95:  int64(percentile(mem, 95)),
		MemBytesMax:  int64(percentile(mem, 100)),
		PIDsMax:      pidsMax,
		HostMemLimit: samples[len(samples)-1].MemLimitBytes,
	}, true
}

// Recommend suggests right-sized limits from observed usage:
//
//   - CPU: limit ≈ p95 usage × 1.3 headroom (rounded up to 0.25 core).
//     Suggested when unset; "increase" when p95 is within 10% of the
//     current limit (the container is being throttled); "decrease" when
//     p95 uses under 30% of it.
//   - Memory: limit ≈ peak usage × 1.25 (rounded up to 64 MiB) — peak,
//     not p95, because exceeding a memory limit is an OOM kill, not a
//     slowdown. "increase" at ≥ 90% of the limit, "decrease" under 40%.
//     A reservation of ≈ median usage is suggested when none is set.
//   - Processes: suggested limit ≈ 2× peak (min 64) when unset, or
//     "increase" when the peak reaches 80% of the current limit.
//
// Returns nil if there's too little history (see minRecommendationSamples).
func Recommend(samples []store.ContainerResourceUsage, current Limits) []Recommendation {
	if len(samples) < minRecommendationSamples {
		return nil
	}
	stats, _ := Summarize(samples)
	var recs []Recommendation

	// CPU. CPUPercent is relative to one core, so 150% = 1.5 cores.
	p95Cores := stats.CPUP95 / 100
	suggestedCPU := int64(roundUp(max(p95Cores*1.3, 0.25), 0.25) * nanoCPUsPerCore)
	curCores := float64(current.NanoCPUs) / nanoCPUsPerCore
	switch {
	case current.NanoCPUs == 0:
		recs = append(recs, Recommendation{
			Resource: "cpu", Action: "set", Severity: SeverityInfo, Suggested: suggestedCPU,
			Message: fmt.Sprintf("No CPU limit is set. Usage peaks around %.2f cores (p95), so a %.2f-core limit leaves headroom while stopping a runaway process from starving its neighbours.",
				p95Cores, float64(suggestedCPU)/nanoCPUsPerCore),
		})
	case p95Cores >= curCores*0.9:
		recs = append(recs, Recommendation{
			Resource: "cpu", Action: "increase", Severity: SeverityWarning,
			Current: current.NanoCPUs, Suggested: max(suggestedCPU, current.NanoCPUs+nanoCPUsPerCore/4),
			Message: fmt.Sprintf("CPU usage (p95 %.2f cores) is at its %.2f-core limit, so the container is likely being throttled.",
				p95Cores, curCores),
		})
	case p95Cores < curCores*0.3 && suggestedCPU < current.NanoCPUs:
		recs = append(recs, Recommendation{
			Resource: "cpu", Action: "decrease", Severity: SeverityInfo,
			Current: current.NanoCPUs, Suggested: suggestedCPU,
			Message: fmt.Sprintf("CPU usage (p95 %.2f cores) uses under 30%% of the %.2f-core limit; lowering it frees capacity for other containers.",
				p95Cores, curCores),
		})
	}

	// Memory.
	peak := float64(stats.MemBytesMax)
	suggestedMem := int64(roundUp(max(peak*1.25, 64*mib), 64*mib))
	switch {
	case current.MemoryLimitBytes == 0:
		recs = append(recs, Recommendation{
			Resource: "memory", Action: "set", Severity: SeverityInfo, Suggested: suggestedMem,
			Message: fmt.Sprintf("No memory limit is set. Peak usage was %s, so a %s limit leaves 25%% headroom and keeps a leak from exhausting host memory.",
				bytesLabel(stats.MemBytesMax), bytesLabel(suggestedMem)),
		})
	case peak >= float64(current.MemoryLimitBytes)*0.9:
		severity := SeverityWarning
		if peak >= float64(current.MemoryLimitBytes)*0.95 {
			severity = SeverityCritical
		}
		recs = append(recs, Recommendation{
			Resource: "memory", Action: "increase", Severity: severity,
			Current: current.MemoryLimitBytes, Suggested: max(suggestedMem, current.MemoryLimitBytes+64*mib),
			Message: fmt.Sprintf("Memory peaked at %s of a %s limit — close to an out-of-memory kill.",
				bytesLabel(stats.MemBytesMax), bytesLabel(current.MemoryLimitBytes)),
		})
	case peak < float64(current.MemoryLimitBytes)*0.4 && suggestedMem < current.MemoryLimitBytes:
		recs = append(recs, Recommendation{
			Resource: "memory", Action: "decrease", Severity: SeverityInfo,
			Current: current.MemoryLimitBytes, Suggested: suggestedMem,
			Message: fmt.Sprintf("Memory peaked at only %s of a %s limit; a %s limit would still leave 25%% headroom.",
				bytesLabel(stats.MemBytesMax), bytesLabel(current.MemoryLimitBytes), bytesLabel(suggestedMem)),
		})
	}
	if current.MemoryReservationBytes == 0 && stats.MemBytesP50 > 0 {
		reservation := int64(roundUp(float64(stats.MemBytesP50), 32*mib))
		limit := current.MemoryLimitBytes
		if limit == 0 {
			limit = suggestedMem
		}
		if reservation < limit {
			recs = append(recs, Recommendation{
				Resource: "memoryReservation", Action: "set", Severity: SeverityInfo, Suggested: reservation,
				Message: fmt.Sprintf("No memory reservation is set. Typical usage is %s; reserving %s helps Docker keep this container's working set when the host is under memory pressure.",
					bytesLabel(stats.MemBytesP50), bytesLabel(reservation)),
			})
		}
	}

	// Processes.
	suggestedPIDs := int64(roundUp(max(float64(stats.PIDsMax)*2, 64), 32))
	switch {
	case current.PidsLimit == 0 && stats.PIDsMax > 0:
		recs = append(recs, Recommendation{
			Resource: "pids", Action: "set", Severity: SeverityInfo, Suggested: suggestedPIDs,
			Message: fmt.Sprintf("No process limit is set. At most %d processes were seen; a limit of %d guards against fork bombs and thread leaks.",
				stats.PIDsMax, suggestedPIDs),
		})
	case current.PidsLimit > 0 && float64(stats.PIDsMax) >= float64(current.PidsLimit)*0.8:
		recs = append(recs, Recommendation{
			Resource: "pids", Action: "increase", Severity: SeverityWarning,
			Current: current.PidsLimit, Suggested: max(suggestedPIDs, current.PidsLimit*2),
			Message: fmt.Sprintf("Process count peaked at %d of a %d limit; new processes/threads will start failing at the limit.",
				stats.PIDsMax, current.PidsLimit),
		})
	}
	return recs
}

// ApplyRecommendations returns current with every recommendation applied —
// the limits an "apply all" action would send.
func ApplyRecommendations(current Limits, recs []Recommendation) Limits {
	out := current
	for _, r := range recs {
		switch r.Resource {
		case "cpu":
			out.NanoCPUs = r.Suggested
		case "memory":
			out.MemoryLimitBytes = r.Suggested
		case "memoryReservation":
			out.MemoryReservationBytes = r.Suggested
		case "pids":
			out.PidsLimit = r.Suggested
		}
	}
	// Docker rejects a reservation above the limit.
	if out.MemoryLimitBytes > 0 && out.MemoryReservationBytes > out.MemoryLimitBytes {
		out.MemoryReservationBytes = out.MemoryLimitBytes
	}
	return out
}

// ---- math helpers ----

func values(samples []store.ContainerResourceUsage, metric string) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = metricValue(s, metric)
	}
	return out
}

func mean64(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func meanStdDev(xs []float64) (mean, sd float64) {
	mean = mean64(xs)
	if len(xs) < 2 {
		return mean, 0
	}
	var ss float64
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(ss / float64(len(xs)-1))
}

// percentile returns the p-th percentile (0–100) by nearest rank.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := slices.Clone(xs)
	slices.Sort(sorted)
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	return sorted[min(max(idx, 0), len(sorted)-1)]
}

// linearFit is ordinary least squares y = a + b·x, returning the slope b
// and the coefficient of determination r².
func linearFit(xs, ys []float64) (slope, r2 float64) {
	mx, my := mean64(xs), mean64(ys)
	var sxy, sxx, syy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return 0, 0
	}
	slope = sxy / sxx
	r := sxy / math.Sqrt(sxx*syy)
	return slope, r * r
}

func roundUp(v, step float64) float64 {
	return math.Ceil(v/step) * step
}

func bytesLabel(b int64) string {
	if b >= 1024*mib {
		return fmt.Sprintf("%.1f GiB", float64(b)/(1024*mib))
	}
	return fmt.Sprintf("%d MiB", b/mib)
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%.1f h", d.Hours())
}
