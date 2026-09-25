package vault

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key, created, err := LoadKey("", filepath.Join(t.TempDir(), "keys", "vault.key"))
	if err != nil || !created {
		t.Fatalf("LoadKey: key=%v created=%v err=%v", key, created, err)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal("hunter2-hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "hunter2") {
		t.Fatal("sealed value contains plaintext")
	}
	got, err := c.Open(sealed)
	if err != nil || got != "hunter2-hunter2" {
		t.Fatalf("Open = %q, %v", got, err)
	}

	other, _ := NewCipher(make([]byte, 32))
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("Open with the wrong key succeeded")
	}
}

func TestLoadKey(t *testing.T) {
	file := filepath.Join(t.TempDir(), "vault.key")
	first, created, err := LoadKey("", file)
	if err != nil || !created {
		t.Fatalf("first LoadKey: created=%v err=%v", created, err)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, %v", info.Mode().Perm(), err)
	}
	second, created, err := LoadKey("", file)
	if err != nil || created || string(first) != string(second) {
		t.Fatalf("second LoadKey: created=%v err=%v same=%v", created, err, string(first) == string(second))
	}

	env := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if key, _, err := LoadKey(env, file); err != nil || len(key) != 32 || string(key) == string(first) {
		t.Errorf("VAULT_KEY should take precedence over the file: %v", err)
	}
	if _, _, err := LoadKey("not base64!", ""); err == nil {
		t.Error("accepted invalid base64")
	}
	if _, _, err := LoadKey("c2hvcnQ=", ""); err == nil {
		t.Error("accepted a short key")
	}
	if _, _, err := LoadKey("", ""); err == nil {
		t.Error("accepted no key source at all")
	}
}

func TestGeneratePassword(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		p, err := GeneratePassword(32)
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 32 {
			t.Fatalf("len = %d", len(p))
		}
		var lo, up, dg bool
		for _, r := range p {
			switch {
			case unicode.IsLower(r):
				lo = true
			case unicode.IsUpper(r):
				up = true
			case unicode.IsDigit(r):
				dg = true
			default:
				t.Fatalf("unexpected character %q in %q", r, p)
			}
		}
		if !lo || !up || !dg {
			t.Fatalf("%q is missing a character class", p)
		}
		if seen[p] {
			t.Fatalf("duplicate password %q", p)
		}
		seen[p] = true
	}
	if p, _ := GeneratePassword(4); len(p) != 16 {
		t.Errorf("short request should be raised to 16, got %d", len(p))
	}
}

func TestReferences(t *testing.T) {
	id, ok := ParseReference(Reference("abc"))
	if !ok || id != "abc" {
		t.Fatalf("ParseReference = %q, %v", id, ok)
	}
	if _, ok := ParseReference("plain"); ok {
		t.Error("plain value parsed as a reference")
	}
	if _, ok := ParseReference("vault:"); ok {
		t.Error("empty reference accepted")
	}
}

func TestRedact(t *testing.T) {
	got := Redact("auth failed for pw=S3cretValue9 (S3cretValue9)", []string{"S3cretValue9", "abc"})
	if strings.Contains(got, "S3cretValue9") {
		t.Fatalf("secret not redacted: %q", got)
	}
	if Redact("abc abc", []string{"abc"}) != "abc abc" {
		t.Error("short secret should not be redacted")
	}
}
