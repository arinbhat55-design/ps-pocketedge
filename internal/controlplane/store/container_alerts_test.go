package store

import (
	"context"
	"testing"
	"time"
)

func TestContainerAlertLifecycle(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	serverID := createTestServer(t, st)

	name := "web"
	ruleID, err := st.CreateContainerAlertRule(ctx, ContainerAlertRule{
		Name: "hot cpu", ServerID: &serverID, ContainerName: &name,
		Metric: "cpu", Threshold: 90, DurationSeconds: 300, Severity: "warning", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteContainerAlertRule(context.Background(), ruleID) })

	rules, err := st.ListContainerAlertRules(ctx, true)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	var found *ContainerAlertRule
	for i := range rules {
		if rules[i].ID == ruleID {
			found = &rules[i]
		}
	}
	if found == nil || !found.Matches(serverID, "web") || found.Matches(serverID, "db") {
		t.Fatalf("rule not listed or scoped wrong: %+v", found)
	}

	threshold := 90.0
	if err := st.OpenContainerAlert(ctx, ContainerAlert{
		Kind: "threshold", RuleID: &ruleID, ServerID: serverID, ContainerID: "c1", ContainerName: name,
		Metric: "cpu", Severity: "warning", Threshold: &threshold, Value: 95, Message: "hot",
	}); err != nil {
		t.Fatalf("open alert: %v", err)
	}

	open, err := st.ListContainerAlerts(ctx, ContainerAlertFilter{OpenOnly: true, ServerID: serverID})
	if err != nil || len(open) != 1 {
		t.Fatalf("want 1 open alert, got %d (err %v)", len(open), err)
	}
	a := open[0]
	if a.RuleName == nil || *a.RuleName != "hot cpu" || a.ServerName != "test-host" {
		t.Errorf("joined names wrong: rule %v server %q", a.RuleName, a.ServerName)
	}

	if err := st.RefreshContainerAlert(ctx, a.ID, "c2", "critical", 99, "hotter"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if err := st.AcknowledgeContainerAlert(ctx, a.ID, ""); err == nil {
		// acknowledged_by is a UUID column; an empty user id must not be accepted.
		t.Error("want an error acknowledging with an invalid user id")
	}

	// Deleting the rule resolves its open alert but keeps it as history.
	if err := st.DeleteContainerAlertRule(ctx, ruleID); err != nil {
		t.Fatalf("delete rule: %v", err)
	}
	all, err := st.ListContainerAlerts(ctx, ContainerAlertFilter{ServerID: serverID})
	if err != nil || len(all) != 1 {
		t.Fatalf("want 1 alert in history, got %d (err %v)", len(all), err)
	}
	if all[0].ResolvedAt == nil || all[0].RuleID != nil || all[0].ContainerID != "c2" || all[0].Severity != "critical" {
		t.Errorf("unexpected alert after rule delete: %+v", all[0])
	}

	if err := st.PruneResolvedContainerAlertsOlderThan(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("prune: %v", err)
	}
	all, _ = st.ListContainerAlerts(ctx, ContainerAlertFilter{ServerID: serverID})
	if len(all) != 0 {
		t.Errorf("want resolved alert pruned, got %d", len(all))
	}
}

func TestListContainerMetricSamplesSince(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	serverID := createTestServer(t, st)
	now := time.Now().UTC()

	for _, s := range []struct {
		id string
		at time.Time
	}{{"a", now.Add(-10 * time.Minute)}, {"a", now.Add(-5 * time.Minute)}, {"b", now.Add(-time.Minute)}, {"b", now.Add(-5 * time.Hour)}} {
		if err := st.InsertContainerMetricSample(ctx, serverID, s.at, ContainerResourceUsage{ContainerID: s.id}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	got, err := st.ListContainerMetricSamplesSince(ctx, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	a, b := got[serverID+"|a"], got[serverID+"|b"]
	if len(a) != 2 || len(b) != 1 || !a[0].RecordedAt.Before(a[1].RecordedAt) {
		t.Errorf("want 2 ordered samples for a and 1 for b, got %d and %d", len(a), len(b))
	}
}
