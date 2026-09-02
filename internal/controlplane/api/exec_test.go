package api

import "testing"

func TestParseResize(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantCols uint32
		wantRows uint32
		wantOK   bool
	}{
		{name: "well-formed", input: "resize:80x24", wantCols: 80, wantRows: 24, wantOK: true},
		{name: "large values", input: "resize:1920x1080", wantCols: 1920, wantRows: 1080, wantOK: true},
		{name: "missing prefix", input: "80x24", wantOK: false},
		{name: "wrong prefix", input: "resiz:80x24", wantOK: false},
		{name: "missing x separator", input: "resize:8024", wantOK: false},
		{name: "non-numeric cols", input: "resize:abcx24", wantOK: false},
		{name: "non-numeric rows", input: "resize:80xabc", wantOK: false},
		{name: "zero cols rejected", input: "resize:0x24", wantOK: false},
		{name: "zero rows rejected", input: "resize:80x0", wantOK: false},
		{name: "negative cols rejected", input: "resize:-1x24", wantOK: false},
		{name: "empty string", input: "", wantOK: false},
		{name: "extra x segments still parses first two", input: "resize:80x24x99", wantOK: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows, ok := parseResize(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if cols != tc.wantCols || rows != tc.wantRows {
				t.Errorf("cols,rows = %d,%d, want %d,%d", cols, rows, tc.wantCols, tc.wantRows)
			}
		})
	}
}
