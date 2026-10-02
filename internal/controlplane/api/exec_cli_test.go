package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
	"github.com/gorilla/websocket"
)

func TestExecCLICommandDispatch(t *testing.T) {
	m := auth.NewManager([]byte("test-secret"))
	token, err := m.IssueToken("u1", "admin@example.com", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	d := deploy.NewDispatcher()
	ch, unregister := d.Register("s1")
	defer unregister()
	relay := deploy.NewExecStreamRelay()
	h := handleContainerExec(slog.New(slog.NewTextHandler(io.Discard, nil)), m, d, relay)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", "s1")
		r.SetPathValue("containerId", "c1")
		h(w, r)
	}))
	defer s.Close()
	q := url.Values{"cmd": {"/bin/sh", "-c", "echo 'two words'"}, "cols": {"120"}, "rows": {"40"}}
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/exec?"+q.Encode(), http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var start *agentv1.ExecStartCommand
	select {
	case msg := <-ch:
		start = msg.GetExecStart()
	case <-time.After(3 * time.Second):
		t.Fatal("no start command")
	}
	if start == nil || !reflect.DeepEqual(start.Cmd, q["cmd"]) || start.Cols != 120 || start.Rows != 40 {
		t.Fatalf("start=%v", start)
	}
	if err = c.WriteMessage(websocket.TextMessage, []byte("resize:90x30")); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-ch:
		input := msg.GetExecInput()
		if input == nil || input.ResizeCols != 90 || input.ResizeRows != 30 {
			t.Fatalf("resize=%v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no resize command")
	}
	relay.Publish(start.RequestId, &agentv1.ExecOutputChunk{RequestId: start.RequestId, Data: []byte("hello"), Done: true, ExitCode: 7})
	_, b, err := c.ReadMessage()
	if err != nil || string(b) != "hello" {
		t.Fatalf("output=%q %v", b, err)
	}
	var end map[string]any
	if err = c.ReadJSON(&end); err != nil || end["exitCode"] != float64(7) {
		t.Fatalf("completion=%v %v", end, err)
	}
}

func TestExecCommandValidation(t *testing.T) {
	for _, args := range [][]string{{""}, {"/bin/sh", "bad\x00arg"}, {strings.Repeat("a", 16385)}, make([]string, 129)} {
		if _, err := parseExecCommand(args); err == nil {
			t.Errorf("accepted invalid command")
		}
	}
	if args, err := parseExecCommand(nil); err != nil || args != nil {
		t.Fatal("default shell should remain supported")
	}
	m := auth.NewManager([]byte("test-secret"))
	token, _ := m.IssueToken("u1", "viewer@example.com", "viewer", 1)
	r := httptest.NewRequest("GET", "/exec", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handleContainerExec(nil, m, nil, nil)(w, r)
	if w.Code != 403 {
		t.Fatalf("viewer exec status=%d", w.Code)
	}
}

func TestStreamTokenPrecedence(t *testing.T) {
	r := httptest.NewRequest("GET", "/stream?token=query", nil)
	if streamToken(r) != "query" {
		t.Fatal("browser query token no longer supported")
	}
	r.Header.Set("Authorization", "Bearer header")
	if streamToken(r) != "header" {
		t.Fatal("header not preferred")
	}
	r.Header.Set("Authorization", "Basic invalid")
	if streamToken(r) != "" {
		t.Fatal("invalid header fell back to query")
	}
}
