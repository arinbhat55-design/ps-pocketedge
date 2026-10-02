package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/auth"
	"github.com/ankitapaul1586-cmd/pspocketedge/internal/controlplane/deploy"
	agentv1 "github.com/ankitapaul1586-cmd/pspocketedge/internal/shared/pb/agentv1"
	"github.com/gorilla/websocket"
)

func TestHostExecAdminDispatchAndDisconnect(t *testing.T) {
	manager := auth.NewManager([]byte("test-secret"))
	token, err := manager.IssueToken("admin", "admin@example.com", "admin", 1)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := deploy.NewDispatcher()
	commands, unregister := dispatcher.Register("host")
	defer unregister()
	relay := deploy.NewExecStreamRelay()
	handler := handleHostExec(slog.New(slog.NewTextHandler(io.Discard, nil)), manager, dispatcher, relay)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", "host")
		handler(w, r)
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var start *agentv1.ExecStartCommand
	select {
	case message := <-commands:
		start = message.GetExecStart()
	case <-time.After(3 * time.Second):
		t.Fatal("no host exec command")
	}
	if start == nil || !start.GetHostShell() || start.GetContainerId() != "" || start.GetServerId() != "host" {
		t.Fatalf("unexpected target: %v", start)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("pwd\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-commands:
		if string(message.GetExecInput().GetData()) != "pwd\n" {
			t.Fatalf("input: %v", message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no shell input")
	}
	relay.Publish(start.RequestId, &agentv1.ExecOutputChunk{RequestId: start.RequestId, Data: []byte("/home/user\n")})
	_, output, err := conn.ReadMessage()
	if err != nil || string(output) != "/home/user\n" {
		t.Fatalf("output: %q %v", output, err)
	}
	conn.Close()
	select {
	case message := <-commands:
		if message.GetStopStream().GetRequestId() != start.RequestId {
			t.Fatalf("missing cleanup: %v", message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("disconnected browser did not stop the host session")
	}
}

func TestHostExecRejectsViewerAndUnauthenticated(t *testing.T) {
	manager := auth.NewManager([]byte("test-secret"))
	token, _ := manager.IssueToken("viewer", "viewer@example.com", "viewer", 1)
	for _, tc := range []struct {
		token  string
		status int
	}{{token, 403}, {"", 401}} {
		request := httptest.NewRequest("GET", "/api/servers/host/exec?token="+tc.token, nil)
		response := httptest.NewRecorder()
		handleHostExec(nil, manager, nil, nil)(response, request)
		if response.Code != tc.status {
			t.Fatalf("status=%d want=%d", response.Code, tc.status)
		}
	}
}
