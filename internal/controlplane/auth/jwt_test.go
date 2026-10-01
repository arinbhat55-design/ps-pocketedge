package auth

import "testing"

func TestLocalTokensAreRevokedWhenLoginIsRequired(t *testing.T) {
	m := NewManager([]byte("test-secret"))
	m.SetLocalSessionsAllowed(true)
	local, err := m.IssueLocalToken("u1", "admin@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	login, err := m.IssueToken("u1", "admin@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}
	m.SetLocalSessionsAllowed(false)
	if _, err := m.ParseToken(local); err == nil {
		t.Fatal("local token remained valid after login was required")
	}
	if _, err := m.ParseToken(login); err != nil {
		t.Fatalf("login token was revoked: %v", err)
	}
	if _, err := m.IssueLocalToken("u1", "admin@example.com", "admin"); err == nil {
		t.Fatal("issued a new local token while login is required")
	}
}
