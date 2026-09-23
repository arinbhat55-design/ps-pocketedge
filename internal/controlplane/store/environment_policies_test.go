package store

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestInMaintenanceWindowSameDay(t *testing.T) {
	// Tuesdays and Thursdays 02:00-04:00 UTC. 2026-09-22 is a Tuesday.
	w := []MaintenanceWindow{{Days: []int{2, 4}, Start: "02:00", End: "04:00"}}
	cases := map[string]bool{
		"2026-09-22T01:59:00Z": false,
		"2026-09-22T02:00:00Z": true,
		"2026-09-22T03:59:00Z": true,
		"2026-09-22T04:00:00Z": false,
		"2026-09-23T03:00:00Z": false, // Wednesday
		"2026-09-24T03:00:00Z": true,  // Thursday
	}
	for at, want := range cases {
		if got := InMaintenanceWindow(w, mustTime(t, at)); got != want {
			t.Errorf("%s: got %v, want %v", at, got, want)
		}
	}
}

func TestInMaintenanceWindowWrapsPastMidnight(t *testing.T) {
	// Saturday 22:00 -> Sunday 02:00. 2026-09-26 is a Saturday.
	w := []MaintenanceWindow{{Days: []int{6}, Start: "22:00", End: "02:00"}}
	if !InMaintenanceWindow(w, mustTime(t, "2026-09-26T23:30:00Z")) {
		t.Error("Saturday 23:30 should be inside")
	}
	if !InMaintenanceWindow(w, mustTime(t, "2026-09-27T01:30:00Z")) {
		t.Error("Sunday 01:30 should be inside the window that started Saturday")
	}
	if InMaintenanceWindow(w, mustTime(t, "2026-09-27T02:30:00Z")) {
		t.Error("Sunday 02:30 should be outside")
	}
}

func TestNextMaintenanceWindow(t *testing.T) {
	w := []MaintenanceWindow{{Days: []int{2}, Start: "02:00", End: "04:00"}}
	next, ok := NextMaintenanceWindow(w, mustTime(t, "2026-09-23T10:00:00Z"))
	if !ok || !next.Equal(mustTime(t, "2026-09-29T02:00:00Z")) {
		t.Fatalf("next = %v, %v", next, ok)
	}
	if _, ok := NextMaintenanceWindow(nil, time.Now()); ok {
		t.Error("no windows should mean no next window")
	}
}

func TestChangeAllowedNow(t *testing.T) {
	var none *EnvironmentPolicy
	if !none.ChangeAllowedNow(time.Now()) {
		t.Error("no policy should allow changes")
	}
	p := &EnvironmentPolicy{EnforceMaintenanceWindow: true, MaintenanceWindows: []MaintenanceWindow{{Days: []int{2}, Start: "02:00", End: "04:00"}}}
	if p.ChangeAllowedNow(mustTime(t, "2026-09-23T10:00:00Z")) {
		t.Error("outside window should block")
	}
	p.EnforceMaintenanceWindow = false
	if !p.ChangeAllowedNow(mustTime(t, "2026-09-23T10:00:00Z")) {
		t.Error("unenforced window should allow")
	}
}

func TestMaintenanceWindowValidate(t *testing.T) {
	bad := []MaintenanceWindow{
		{Days: nil, Start: "01:00", End: "02:00"},
		{Days: []int{7}, Start: "01:00", End: "02:00"},
		{Days: []int{1}, Start: "25:00", End: "02:00"},
	}
	for _, w := range bad {
		if w.Validate() == nil {
			t.Errorf("expected %+v to be invalid", w)
		}
	}
	if err := (MaintenanceWindow{Days: []int{0, 6}, Start: "22:00", End: "02:00"}).Validate(); err != nil {
		t.Errorf("valid window rejected: %v", err)
	}
}
