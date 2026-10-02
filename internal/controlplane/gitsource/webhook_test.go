package gitsource

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"
)

func sign(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookGitHubSignature(t *testing.T) {
	body := `{"ref":"refs/heads/main"}`
	r := httptest.NewRequest("POST", "/hook", strings.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", sign(body, "s3cret"))
	if err := VerifyWebhook(r, []byte(body), "s3cret"); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := VerifyWebhook(r, []byte(body), "other"); err == nil {
		t.Fatal("wrong secret accepted")
	}
}

func TestVerifyWebhookGitLabTokenAndQuerySecret(t *testing.T) {
	r := httptest.NewRequest("POST", "/hook", nil)
	r.Header.Set("X-Gitlab-Token", "s3cret")
	if err := VerifyWebhook(r, nil, "s3cret"); err != nil {
		t.Fatalf("gitlab token rejected: %v", err)
	}
	q := httptest.NewRequest("POST", "/hook?secret=s3cret", nil)
	if err := VerifyWebhook(q, nil, "s3cret"); err != nil {
		t.Fatalf("query secret rejected: %v", err)
	}
	none := httptest.NewRequest("POST", "/hook", nil)
	if err := VerifyWebhook(none, nil, "s3cret"); err == nil {
		t.Fatal("unauthenticated webhook accepted")
	}
}

func TestParsePushEventProviders(t *testing.T) {
	cases := []struct {
		name    string
		header  [2]string
		body    string
		wantRef string
		wantSHA string
	}{
		{"github", [2]string{"X-GitHub-Event", "push"}, `{"ref":"refs/heads/main","after":"abc123"}`, "main", "abc123"},
		{"gitlab tag", [2]string{"X-Gitlab-Event", "Tag Push Hook"}, `{"object_kind":"tag_push","ref":"refs/tags/v1.0","checkout_sha":"def456"}`, "v1.0", "def456"},
		{"bitbucket", [2]string{"X-Event-Key", "repo:push"}, `{"push":{"changes":[{"new":{"name":"develop","target":{"hash":"789abc"}}}]}}`, "develop", "789abc"},
		{"azure", [2]string{"", ""}, `{"eventType":"git.push","resource":{"refUpdates":[{"name":"refs/heads/release","newObjectId":"fedcba"}]}}`, "release", "fedcba"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/hook", nil)
		if c.header[0] != "" {
			r.Header.Set(c.header[0], c.header[1])
		}
		ev, err := ParsePushEvent(r, []byte(c.body))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(ev.Refs) != 1 || ev.Refs[0].Name != c.wantRef || ev.Refs[0].Commit != c.wantSHA {
			t.Errorf("%s: got %+v", c.name, ev.Refs)
		}
	}
}

func TestParsePushEventIgnoresPingAndDeletes(t *testing.T) {
	ping := httptest.NewRequest("POST", "/hook", nil)
	ping.Header.Set("X-GitHub-Event", "ping")
	if ev, _ := ParsePushEvent(ping, []byte(`{}`)); len(ev.Refs) != 0 {
		t.Errorf("ping produced refs: %+v", ev.Refs)
	}
	del := httptest.NewRequest("POST", "/hook", nil)
	del.Header.Set("X-GitHub-Event", "push")
	if ev, _ := ParsePushEvent(del, []byte(`{"ref":"refs/heads/old","after":"`+zeroCommit+`"}`)); len(ev.Refs) != 0 {
		t.Errorf("branch deletion produced refs: %+v", ev.Refs)
	}
}
