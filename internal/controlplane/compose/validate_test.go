package compose

import (
	"strings"
	"testing"
)

func containsSubstring(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestParseRejectsUndefinedDependency(t *testing.T) {
	r := Parse(`services:
  web:
    image: nginx
    depends_on: [db]
`)
	if r.Valid || !containsSubstring(r.Errors, `undefined service "db"`) {
		t.Fatalf("got valid=%v errors=%v", r.Valid, r.Errors)
	}
}

func TestParseRejectsDependencyCycle(t *testing.T) {
	r := Parse(`services:
  a:
    image: x
    depends_on:
      b:
        condition: service_started
  b:
    image: x
    depends_on: [c]
  c:
    image: x
    depends_on: [a]
`)
	if r.Valid || !containsSubstring(r.Errors, "dependency cycle: a -> b -> c -> a") {
		t.Fatalf("got valid=%v errors=%v", r.Valid, r.Errors)
	}
}

func TestParseRejectsSelfDependency(t *testing.T) {
	r := Parse(`services:
  a:
    image: x
    depends_on: [a]
`)
	if r.Valid || !containsSubstring(r.Errors, "depends on itself") {
		t.Fatalf("got valid=%v errors=%v", r.Valid, r.Errors)
	}
}

func TestParseAcceptsValidDependencies(t *testing.T) {
	r := Parse(`services:
  web:
    image: nginx
    depends_on:
      db:
        condition: service_healthy
  db:
    image: postgres
`)
	if !r.Valid {
		t.Fatalf("errors = %v", r.Errors)
	}
}

func TestValidateWarnsAboutConflictsAndUnsupportedSettings(t *testing.T) {
	r := Parse(`services:
  a:
    image: x
    container_name: fixed
    ports: ["8080:80"]
    volumes: ["./data:/data", "undeclared:/cache"]
    networks: [backend]
  b:
    build: .
    ports:
      - target: 81
        published: "8080"
    deploy:
      replicas: 3
secrets:
  s: {}
`)
	if !r.Valid {
		t.Fatalf("warnings shouldn't make the file invalid: %v", r.Errors)
	}
	for _, want := range []string{
		`host port 8080/tcp is published by more than one service (a, b)`,
		`"container_name"`,
		`bind mounts aren't supported`,
		`volume "undeclared"`,
		`network "backend"`,
		`uses build`,
		`runs 3 replicas but publishes fixed host port 8080`,
		`top-level "secrets"`,
	} {
		if !containsSubstring(r.Warnings, want) {
			t.Errorf("missing warning containing %q in %v", want, r.Warnings)
		}
	}
}
