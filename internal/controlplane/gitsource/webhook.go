package gitsource

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// PushEvent is the provider-independent part of a push webhook: which
// branches/tags moved, and to which commit.
type PushEvent struct {
	Refs []PushedRef
}

// PushedRef is one branch or tag a push updated.
type PushedRef struct {
	Name   string // short name, e.g. "main" or "v1.2.0"
	Commit string
}

// VerifyWebhook authenticates an inbound webhook against secret, accepting
// whichever mechanism the provider uses:
//   - GitHub / Bitbucket Cloud: HMAC-SHA256 of the body in
//     X-Hub-Signature-256 or X-Hub-Signature ("sha256=<hex>");
//   - GitLab: the secret verbatim in X-Gitlab-Token;
//   - Azure DevOps (and anything else): HTTP basic auth password, or a
//     ?secret= query parameter on the webhook URL.
func VerifyWebhook(r *http.Request, body []byte, secret string) error {
	if secret == "" {
		return errors.New("repository has no webhook secret")
	}
	for _, header := range []string{"X-Hub-Signature-256", "X-Hub-Signature"} {
		if sig := r.Header.Get(header); strings.HasPrefix(sig, "sha256=") {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			expected := hex.EncodeToString(mac.Sum(nil))
			if hmac.Equal([]byte(strings.TrimPrefix(sig, "sha256=")), []byte(expected)) {
				return nil
			}
			return errors.New("webhook signature mismatch")
		}
	}
	if token := r.Header.Get("X-Gitlab-Token"); token != "" {
		return constantTimeCheck(token, secret)
	}
	if _, pass, ok := r.BasicAuth(); ok {
		return constantTimeCheck(pass, secret)
	}
	if q := r.URL.Query().Get("secret"); q != "" {
		return constantTimeCheck(q, secret)
	}
	return errors.New("webhook is missing a signature or secret")
}

func constantTimeCheck(got, want string) error {
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
		return nil
	}
	return errors.New("webhook secret mismatch")
}

func shortRef(ref string) string {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	return strings.TrimPrefix(ref, "refs/tags/")
}

const zeroCommit = "0000000000000000000000000000000000000000"

// ParsePushEvent extracts the pushed refs from a GitHub, GitLab, Bitbucket
// Cloud, or Azure DevOps push payload. A ping/test event or anything that
// isn't a push yields no refs rather than an error. Deleted refs are
// skipped.
func ParsePushEvent(r *http.Request, body []byte) (PushEvent, error) {
	var ev PushEvent
	switch {
	case r.Header.Get("X-GitHub-Event") != "":
		if r.Header.Get("X-GitHub-Event") != "push" {
			return ev, nil
		}
		var p struct {
			Ref   string `json:"ref"`
			After string `json:"after"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return ev, err
		}
		if p.After != zeroCommit {
			ev.Refs = append(ev.Refs, PushedRef{Name: shortRef(p.Ref), Commit: p.After})
		}
	case r.Header.Get("X-Gitlab-Event") != "":
		var p struct {
			ObjectKind  string `json:"object_kind"`
			Ref         string `json:"ref"`
			After       string `json:"after"`
			CheckoutSHA string `json:"checkout_sha"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return ev, err
		}
		if p.ObjectKind != "push" && p.ObjectKind != "tag_push" {
			return ev, nil
		}
		commit := p.CheckoutSHA
		if commit == "" {
			commit = p.After
		}
		if commit != "" && commit != zeroCommit {
			ev.Refs = append(ev.Refs, PushedRef{Name: shortRef(p.Ref), Commit: commit})
		}
	case r.Header.Get("X-Event-Key") != "":
		if r.Header.Get("X-Event-Key") != "repo:push" {
			return ev, nil
		}
		var p struct {
			Push struct {
				Changes []struct {
					New *struct {
						Name   string `json:"name"`
						Target struct {
							Hash string `json:"hash"`
						} `json:"target"`
					} `json:"new"`
				} `json:"changes"`
			} `json:"push"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return ev, err
		}
		for _, c := range p.Push.Changes {
			if c.New != nil {
				ev.Refs = append(ev.Refs, PushedRef{Name: c.New.Name, Commit: c.New.Target.Hash})
			}
		}
	default:
		// Azure DevOps "git.push" service hook.
		var p struct {
			EventType string `json:"eventType"`
			Resource  struct {
				RefUpdates []struct {
					Name        string `json:"name"`
					NewObjectID string `json:"newObjectId"`
				} `json:"refUpdates"`
			} `json:"resource"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return ev, err
		}
		if p.EventType != "git.push" {
			return ev, nil
		}
		for _, u := range p.Resource.RefUpdates {
			if u.NewObjectID != zeroCommit {
				ev.Refs = append(ev.Refs, PushedRef{Name: shortRef(u.Name), Commit: u.NewObjectID})
			}
		}
	}
	return ev, nil
}
