package admin

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testSnapshot() Snapshot {
	return Snapshot{
		GeneratedAt:       time.Unix(100, 0).UTC(),
		Environment:       "test",
		UptimeSeconds:     12.5,
		OnlinePlayers:     2,
		ActiveRooms:       1,
		MatchQueuePlayers: 0,
		ActiveMonsters:    3,
		Rooms: []RoomStatus{{RoomID: 7, StageIndex: 2, StageSeed: -42, Phase: "playing", ServerTick: 90,
			Players: 2, Monsters: 3, InputQueueDepth: 1, TickWorkMillis: 0.4}},
		RecentDirectorDecisions: []DirectorDecision{{RoomID: 7, StageIndex: 2, Seed: -42, NewDifficulty: 1.1,
			Reasons: []string{"base progression +3%"}}},
	}
}

func TestReadOnlyJSONEndpoints(t *testing.T) {
	server, err := New(ProviderFunc(testSnapshot), 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path string
		key  string
	}{
		{"/healthz", "status"},
		{"/api/status", "online_players"},
		{"/api/rooms", "rooms"},
		{"/api/director/recent", "decisions"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", test.path, response.Code)
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("GET %s missing safety headers: %v", test.path, response.Header())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("GET %s invalid JSON: %v", test.path, err)
		}
		if _, ok := body[test.key]; !ok {
			t.Fatalf("GET %s missing %q: %v", test.path, test.key, body)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/status", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/status status = %d, want 405", response.Code)
	}
}

func TestWebSocketStreamsSnapshot(t *testing.T) {
	adminServer, err := New(ProviderFunc(testSnapshot), 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(adminServer.Handler())
	defer httpServer.Close()
	defer adminServer.Close()

	parsed, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", parsed.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(2 * time.Second))
	key := "MDEyMzQ1Njc4OWFiY2RlZg=="
	request := fmt.Sprintf("GET /ws HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nOrigin: http://127.0.0.1:5173\r\n\r\n", parsed.Host, key)
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("websocket status = %d", response.StatusCode)
	}
	payload, err := readServerTextFrame(reader)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.OnlinePlayers != 2 || len(snapshot.Rooms) != 1 || snapshot.Rooms[0].RoomID != 7 {
		t.Fatalf("unexpected streamed snapshot: %+v", snapshot)
	}
}

func TestWebSocketRejectsRemoteBrowserOrigin(t *testing.T) {
	server, err := New(ProviderFunc(testSnapshot), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/ws", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "MDEyMzQ1Njc4OWFiY2RlZg==")
	request.Header.Set("Origin", "https://example.com")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "origin") {
		t.Fatalf("remote origin response = %d %q", response.Code, response.Body.String())
	}
}

func TestWriteTextFrameLengths(t *testing.T) {
	for _, size := range []int{1, 125, 126, 65535, 65536} {
		payload := bytes.Repeat([]byte{'x'}, size)
		var wire bytes.Buffer
		if err := writeTextFrame(&wire, payload); err != nil {
			t.Fatal(err)
		}
		got, err := readServerTextFrame(bufio.NewReader(&wire))
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("size %d payload changed", size)
		}
	}
}

func readServerTextFrame(reader *bufio.Reader) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	if header[0] != 0x81 || header[1]&0x80 != 0 {
		return nil, fmt.Errorf("unexpected frame header %x", header)
	}
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(reader, extended); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(reader, extended); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(extended)
	}
	payload := make([]byte, int(length))
	_, err := io.ReadFull(reader, payload)
	return payload, err
}
