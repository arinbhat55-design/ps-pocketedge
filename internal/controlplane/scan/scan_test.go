package scan

import "testing"

func TestSummarizeCountsBySeverity(t *testing.T) {
	out := trivyOutput{}
	out.Results = append(out.Results, struct {
		Vulnerabilities []struct {
			VulnerabilityID string `json:"VulnerabilityID"`
			PkgName         string `json:"PkgName"`
			Severity        string `json:"Severity"`
			Title           string `json:"Title"`
			FixedVersion    string `json:"FixedVersion"`
		} `json:"Vulnerabilities"`
	}{
		Vulnerabilities: []struct {
			VulnerabilityID string `json:"VulnerabilityID"`
			PkgName         string `json:"PkgName"`
			Severity        string `json:"Severity"`
			Title           string `json:"Title"`
			FixedVersion    string `json:"FixedVersion"`
		}{
			{VulnerabilityID: "CVE-1", Severity: "CRITICAL"},
			{VulnerabilityID: "CVE-2", Severity: "HIGH"},
			{VulnerabilityID: "CVE-3", Severity: "HIGH"},
			{VulnerabilityID: "CVE-4", Severity: "MEDIUM"},
			{VulnerabilityID: "CVE-5", Severity: "LOW"},
			{VulnerabilityID: "CVE-6", Severity: "SOMETHING_UNEXPECTED"},
		},
	})

	result := summarize(out)

	if result.CriticalCount != 1 {
		t.Errorf("CriticalCount = %d, want 1", result.CriticalCount)
	}
	if result.HighCount != 2 {
		t.Errorf("HighCount = %d, want 2", result.HighCount)
	}
	if result.MediumCount != 1 {
		t.Errorf("MediumCount = %d, want 1", result.MediumCount)
	}
	if result.LowCount != 1 {
		t.Errorf("LowCount = %d, want 1", result.LowCount)
	}
	if result.UnknownCount != 1 {
		t.Errorf("UnknownCount = %d, want 1 (unrecognized severity strings fall into Unknown)", result.UnknownCount)
	}
	if len(result.Vulnerabilities) != 6 {
		t.Errorf("len(Vulnerabilities) = %d, want 6", len(result.Vulnerabilities))
	}
}

func TestSummarizeCapsVulnerabilityList(t *testing.T) {
	type vuln = struct {
		VulnerabilityID string `json:"VulnerabilityID"`
		PkgName         string `json:"PkgName"`
		Severity        string `json:"Severity"`
		Title           string `json:"Title"`
		FixedVersion    string `json:"FixedVersion"`
	}

	vulns := make([]vuln, maxVulnerabilities+25)
	for i := range vulns {
		vulns[i] = vuln{VulnerabilityID: "CVE-X", Severity: "LOW"}
	}

	out := trivyOutput{}
	out.Results = append(out.Results, struct {
		Vulnerabilities []vuln `json:"Vulnerabilities"`
	}{Vulnerabilities: vulns})

	result := summarize(out)

	if len(result.Vulnerabilities) != maxVulnerabilities {
		t.Errorf("len(Vulnerabilities) = %d, want the list capped at %d", len(result.Vulnerabilities), maxVulnerabilities)
	}
	// The severity counts must still reflect every finding, not just the
	// capped list — the cap only bounds the response payload's size, not
	// the counts the UI's severity chips render.
	if result.LowCount != maxVulnerabilities+25 {
		t.Errorf("LowCount = %d, want %d (counts uncapped even though the list is capped)", result.LowCount, maxVulnerabilities+25)
	}
}

func TestSummarizeEmptyReport(t *testing.T) {
	result := summarize(trivyOutput{})

	total := result.CriticalCount + result.HighCount + result.MediumCount + result.LowCount + result.UnknownCount
	if total != 0 {
		t.Errorf("expected zero vulnerabilities for an empty report, got %+v", result)
	}
	if len(result.Vulnerabilities) != 0 {
		t.Errorf("expected an empty vulnerability list, got %d entries", len(result.Vulnerabilities))
	}
}
