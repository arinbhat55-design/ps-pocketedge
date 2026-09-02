// Package scan runs vulnerability scans against image references by
// shelling out to the Trivy CLI (https://trivy.dev), rather than embedding
// it as a library — Trivy's own release process is the CLI, and this way
// the control plane just needs the binary on its PATH, upgradable
// independent of a Go module bump. Scanning happens against the registry
// reference directly (Trivy pulls it itself), not against an already-
// pulled image on some agent, so it works even for images not yet
// deployed anywhere.
package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"
)

// ErrScannerUnavailable means the trivy binary isn't installed/on PATH —
// callers should surface this as a clear "scanning not available" error
// rather than a generic failure.
var ErrScannerUnavailable = errors.New("trivy is not installed on the control plane host")

// scanTimeout bounds one trivy invocation — a cold scan pulls the image
// and its vulnerability DB lookups, which can take a while for a large
// image, but must not hang a request indefinitely.
const scanTimeout = 5 * time.Minute

// maxVulnerabilities caps how many individual vulnerabilities are kept in
// Result.Vulnerabilities — a heavily-outdated base image can report
// thousands, and the dashboard only needs enough to be useful, not a full
// dump of Trivy's raw output.
const maxVulnerabilities = 50

// Vulnerability is one summarized finding.
type Vulnerability struct {
	ID           string `json:"id"`
	PkgName      string `json:"pkgName"`
	Severity     string `json:"severity"`
	Title        string `json:"title,omitempty"`
	FixedVersion string `json:"fixedVersion,omitempty"`
}

// Result summarizes a Trivy image scan: severity counts across every
// finding, plus a capped list of the individual vulnerabilities.
type Result struct {
	CriticalCount   int             `json:"criticalCount"`
	HighCount       int             `json:"highCount"`
	MediumCount     int             `json:"mediumCount"`
	LowCount        int             `json:"lowCount"`
	UnknownCount    int             `json:"unknownCount"`
	Vulnerabilities []Vulnerability `json:"vulnerabilities"`
}

// trivyOutput mirrors the subset of `trivy image --format json` this
// package reads.
type trivyOutput struct {
	Results []struct {
		Vulnerabilities []struct {
			VulnerabilityID string `json:"VulnerabilityID"`
			PkgName         string `json:"PkgName"`
			Severity        string `json:"Severity"`
			Title           string `json:"Title"`
			FixedVersion    string `json:"FixedVersion"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

// Run scans ref with Trivy and returns a summarized Result. Returns
// ErrScannerUnavailable if the trivy binary isn't found.
func Run(ctx context.Context, ref string) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "trivy", "image", "--format", "json", "--quiet", ref)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrScannerUnavailable
		}
		if stderr.Len() > 0 {
			return nil, errors.New("trivy scan failed: " + stderr.String())
		}
		return nil, err
	}

	var out trivyOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, errors.New("failed to parse trivy output: " + err.Error())
	}
	return summarize(out), nil
}

// summarize reduces a trivy JSON report down to severity counts plus a
// capped vulnerability list — factored out of Run so this pure
// transformation is unit-testable without shelling out to trivy.
func summarize(out trivyOutput) *Result {
	result := &Result{}
	for _, r := range out.Results {
		for _, v := range r.Vulnerabilities {
			switch v.Severity {
			case "CRITICAL":
				result.CriticalCount++
			case "HIGH":
				result.HighCount++
			case "MEDIUM":
				result.MediumCount++
			case "LOW":
				result.LowCount++
			default:
				result.UnknownCount++
			}
			if len(result.Vulnerabilities) < maxVulnerabilities {
				result.Vulnerabilities = append(result.Vulnerabilities, Vulnerability{
					ID:           v.VulnerabilityID,
					PkgName:      v.PkgName,
					Severity:     v.Severity,
					Title:        v.Title,
					FixedVersion: v.FixedVersion,
				})
			}
		}
	}
	return result
}
