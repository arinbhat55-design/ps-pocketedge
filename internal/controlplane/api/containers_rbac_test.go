package api

import (
	"reflect"
	"testing"
)

func TestRedactEnvValues(t *testing.T) {
	original := []string{"TOKEN=secret=value", "EMPTY=", "FLAG"}
	want := []string{"TOKEN=••••", "EMPTY=••••", "FLAG=••••"}
	got := redactEnvValues(original)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("redacted = %v, want %v", got, want)
	}
	if original[0] != "TOKEN=secret=value" {
		t.Fatal("redaction changed original environment")
	}
}
