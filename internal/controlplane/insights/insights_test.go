package insights

import (
	"testing"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/store"
)

var testNow = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// series builds n samples 20s apart ending at testNow, with fill setting
// each sample's fields from its index.
func series(n int, fill func(i int, s *store.ContainerResourceUsage)) []store.ContainerResourceUsage {
	out := make([]store.ContainerResourceUsage, n)
	for i := range out {
		out[i].RecordedAt = testNow.Add(-time.Duration(n-1-i) * 20 * time.Second)
		out[i].MemLimitBytes = 1024 * mib
		fill(i, &out[i])
	}
	return out
}

func TestDetectAnomaliesCPUSpike(t *testing.T) {
	// 1 hour at ~10% CPU (with a little noise), then the last 5 minutes at 80%.
	samples := series(180, func(i int, s *store.ContainerResourceUsage) {
		s.CPUPercent = 10 + float64(i%3)
		if i >= 165 {
			s.CPUPercent = 80
		}
	})
	anomalies := DetectAnomalies(samples, testNow)
	if len(anomalies) != 1 || anomalies[0].Metric != MetricCPU || anomalies[0].Kind != "spike" {
		t.Fatalf("want one CPU spike, got %+v", anomalies)
	}
	if anomalies[0].Severity != SeverityCritical {
		t.Errorf("a 70-point jump over a flat baseline should be critical, got %s", anomalies[0].Severity)
	}
}

func TestDetectAnomaliesIgnoresSmallJumps(t *testing.T) {
	// Flat at 0.1% then 2% — many sigma, but not a meaningful absolute change.
	samples := series(180, func(i int, s *store.ContainerResourceUsage) {
		s.CPUPercent = 0.1
		if i >= 165 {
			s.CPUPercent = 2
		}
	})
	if got := DetectAnomalies(samples, testNow); len(got) != 0 {
		t.Fatalf("want no anomalies, got %+v", got)
	}
}

func TestDetectAnomaliesNeedsBaseline(t *testing.T) {
	samples := series(20, func(i int, s *store.ContainerResourceUsage) { s.CPUPercent = float64(i * 10) })
	if got := DetectAnomalies(samples, testNow); len(got) != 0 {
		t.Fatalf("want no anomalies without enough history, got %+v", got)
	}
}

func TestDetectAnomaliesMemoryLeak(t *testing.T) {
	// 2 hours growing linearly from 200 MiB to 800 MiB of a 1 GiB limit.
	n := 360
	samples := series(n, func(i int, s *store.ContainerResourceUsage) {
		s.MemUsageBytes = int64(200*mib + float64(i)/float64(n-1)*600*mib)
		s.MemPercent = float64(s.MemUsageBytes) / float64(s.MemLimitBytes) * 100
	})
	var leak *Anomaly
	for _, a := range DetectAnomalies(samples, testNow) {
		if a.Kind == "leak" {
			leak = &a
		}
	}
	if leak == nil {
		t.Fatal("want a memory leak anomaly")
	}
	if leak.Severity != SeverityCritical {
		t.Errorf("reaching the limit in ~48 min should be critical, got %s", leak.Severity)
	}
}

func TestEvaluateThreshold(t *testing.T) {
	high := series(30, func(i int, s *store.ContainerResourceUsage) { s.CPUPercent = 95 })
	if !EvaluateThreshold(high, MetricCPU, 90, 5*time.Minute, testNow).Breaching {
		t.Error("sustained 95% should breach a 90%/5m rule")
	}

	dip := series(30, func(i int, s *store.ContainerResourceUsage) {
		s.CPUPercent = 95
		if i == 25 {
			s.CPUPercent = 50
		}
	})
	if EvaluateThreshold(dip, MetricCPU, 90, 5*time.Minute, testNow).Breaching {
		t.Error("a dip below threshold inside the window should not breach")
	}

	short := series(3, func(i int, s *store.ContainerResourceUsage) { s.CPUPercent = 95 })
	if EvaluateThreshold(short, MetricCPU, 90, 10*time.Minute, testNow).Breaching {
		t.Error("one minute of history should not satisfy a 10 minute rule")
	}

	if EvaluateThreshold(high, MetricCPU, 90, 5*time.Minute, testNow.Add(10*time.Minute)).Breaching {
		t.Error("stale samples should not breach")
	}

	if !EvaluateThreshold(short, MetricCPU, 90, 0, testNow).Breaching {
		t.Error("a zero-duration rule should breach on the latest sample")
	}
}

func TestRecommend(t *testing.T) {
	// ~0.5 cores, 300 MiB peak, 20 processes, over 1 hour.
	samples := series(180, func(i int, s *store.ContainerResourceUsage) {
		s.CPUPercent = 40 + float64(i%10)
		s.MemUsageBytes = 250*mib + int64(i%5)*10*mib
		s.PIDs = 20
	})

	t.Run("unset limits", func(t *testing.T) {
		recs := Recommend(samples, Limits{})
		byResource := map[string]Recommendation{}
		for _, r := range recs {
			byResource[r.Resource] = r
		}
		if r := byResource["cpu"]; r.Action != "set" || r.Suggested != 750_000_000 {
			t.Errorf("cpu: want set 0.75 cores, got %+v", r)
		}
		if r := byResource["memory"]; r.Action != "set" || r.Suggested != 384*mib {
			t.Errorf("memory: want set 384 MiB, got %+v", r)
		}
		if r := byResource["pids"]; r.Action != "set" || r.Suggested != 64 {
			t.Errorf("pids: want set 64, got %+v", r)
		}
		if _, ok := byResource["memoryReservation"]; !ok {
			t.Error("want a memory reservation suggestion")
		}
	})

	t.Run("tight limits", func(t *testing.T) {
		recs := Recommend(samples, Limits{NanoCPUs: 500_000_000, MemoryLimitBytes: 300 * mib, MemoryReservationBytes: 100 * mib, PidsLimit: 22})
		for _, r := range recs {
			if r.Action != "increase" {
				t.Errorf("want only increases, got %+v", r)
			}
		}
		if len(recs) != 3 {
			t.Errorf("want cpu, memory, and pids increases, got %+v", recs)
		}
	})

	t.Run("oversized limits", func(t *testing.T) {
		recs := Recommend(samples, Limits{NanoCPUs: 4_000_000_000, MemoryLimitBytes: 4096 * mib, MemoryReservationBytes: 100 * mib, PidsLimit: 500})
		if len(recs) != 2 || recs[0].Action != "decrease" || recs[1].Action != "decrease" {
			t.Errorf("want cpu and memory decreases, got %+v", recs)
		}
	})

	t.Run("too little history", func(t *testing.T) {
		if recs := Recommend(samples[:30], Limits{}); recs != nil {
			t.Errorf("want nil, got %+v", recs)
		}
	})
}

func TestApplyRecommendationsCapsReservation(t *testing.T) {
	out := ApplyRecommendations(Limits{MemoryReservationBytes: 512 * mib}, []Recommendation{{Resource: "memory", Suggested: 256 * mib}})
	if out.MemoryReservationBytes != 256*mib {
		t.Errorf("reservation should be capped at the new limit, got %d", out.MemoryReservationBytes)
	}
}

func TestConditions(t *testing.T) {
	server := "srv-1"
	name := "web"
	rules := []store.ContainerAlertRule{
		{ID: "r1", Name: "hot cpu", Metric: MetricCPU, Threshold: 90, DurationSeconds: 60, Severity: SeverityCritical},
		{ID: "r2", Name: "other container", ContainerName: new(string), Metric: MetricCPU, Threshold: 10, Severity: SeverityWarning},
	}
	*rules[1].ContainerName = "db"
	containers := []store.FleetContainer{{ServerID: server, ContainerID: "c1", Name: name}}
	samples := map[string][]store.ContainerResourceUsage{
		server + "|c1": series(10, func(i int, s *store.ContainerResourceUsage) { s.CPUPercent = 95 }),
	}
	firing := Conditions(rules, containers, samples, testNow)
	if len(firing) != 1 {
		t.Fatalf("want exactly the matching rule firing, got %+v", firing)
	}
	for _, a := range firing {
		if a.RuleID == nil || *a.RuleID != "r1" || a.Severity != SeverityCritical || a.ContainerName != name {
			t.Errorf("unexpected alert %+v", a)
		}
	}
}

func TestFormatValue(t *testing.T) {
	for _, tc := range []struct {
		metric string
		v      float64
		want   string
	}{
		{MetricCPU, 150, "1.50 cores"},
		{MetricMemory, 42, "42.0%"},
		{MetricPIDs, 12, "12 processes"},
	} {
		if got := FormatValue(tc.metric, tc.v); got != tc.want {
			t.Errorf("FormatValue(%s, %v) = %q, want %q", tc.metric, tc.v, got, tc.want)
		}
	}
}
