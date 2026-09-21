// Package admin 暴露只读运维 HTTP/WebSocket API，只负责传输与 JSON 结构；
// 权威数值由 gameserver 集成层通过 Provider 提供。
package admin

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

var ErrNilProvider = errors.New("admin: nil snapshot provider")

type RoomStatus struct {
	RoomID             uint64  `json:"room_id"`
	StageIndex         uint32  `json:"stage_index"`
	StageSeed          int64   `json:"stage_seed"`
	Phase              string  `json:"phase"`
	ServerTick         uint64  `json:"server_tick"`
	Players            int     `json:"players"`
	Monsters           int     `json:"monsters"`
	Projectiles        int     `json:"projectiles"`
	ControlQueueDepth  int     `json:"control_queue_depth"`
	InputQueueDepth    int     `json:"input_queue_depth"`
	QueueRejections    uint64  `json:"queue_rejections"`
	RejectedInputs     uint64  `json:"rejected_inputs"`
	DroppedSnapshots   uint64  `json:"dropped_snapshots"`
	DroppedTickSamples uint64  `json:"dropped_tick_samples"`
	TickWorkMillis     float64 `json:"tick_work_ms"`
}

type DirectorDecision struct {
	ObservedAt         time.Time `json:"observed_at"`
	RoomID             uint64    `json:"room_id"`
	StageIndex         uint32    `json:"stage_index"`
	Seed               int64     `json:"seed"`
	ClearTimeSeconds   float64   `json:"clear_time_seconds"`
	TeamHPPercent      float64   `json:"team_hp_percent"`
	AverageDPS         float64   `json:"average_dps"`
	DeathCount         int       `json:"death_count"`
	DamageTaken        float64   `json:"damage_taken"`
	EquipmentPower     float64   `json:"equipment_power"`
	PreviousDifficulty float64   `json:"previous_difficulty"`
	NewDifficulty      float64   `json:"new_difficulty"`
	Adjustment         float64   `json:"adjustment"`
	MonsterCount       int       `json:"monster_count"`
	DurationMicros     int64     `json:"duration_us"`
	Reasons            []string  `json:"reasons"`
}

type QueueStatus struct {
	ControlDepth       int    `json:"control_depth"`
	InputDepth         int    `json:"input_depth"`
	Rejections         uint64 `json:"rejections"`
	RejectedInputs     uint64 `json:"rejected_inputs"`
	DroppedSnapshots   uint64 `json:"dropped_snapshots"`
	DroppedTickSamples uint64 `json:"dropped_tick_samples"`
}

type NetworkStatus struct {
	ActiveConnections      int    `json:"active_connections"`
	ReliableQueueDepth     int    `json:"reliable_queue_depth"`
	ReliableQueueCapacity  int    `json:"reliable_queue_capacity"`
	SnapshotsPending       int    `json:"snapshots_pending"`
	ReliableSendRejections uint64 `json:"reliable_send_rejections"`
	SnapshotReplacements   uint64 `json:"snapshot_replacements"`
}

// Snapshot 专用于只读运维，不包含恢复令牌、凭据、玩家名或客户端控制的原始值。
type Snapshot struct {
	GeneratedAt             time.Time          `json:"generated_at"`
	Environment             string             `json:"environment"`
	UptimeSeconds           float64            `json:"uptime_seconds"`
	OnlinePlayers           int                `json:"online_players"`
	ActiveRooms             int                `json:"active_rooms"`
	MatchQueuePlayers       int                `json:"match_queue_players"`
	ActiveMonsters          int                `json:"active_monsters"`
	ActiveProjectiles       int                `json:"active_projectiles"`
	RoomQueues              QueueStatus        `json:"room_queues"`
	Network                 NetworkStatus      `json:"network"`
	Rooms                   []RoomStatus       `json:"rooms"`
	RecentDirectorDecisions []DirectorDecision `json:"recent_director_decisions"`
}

type Provider interface {
	Snapshot() Snapshot
}

type ProviderFunc func() Snapshot

func (f ProviderFunc) Snapshot() Snapshot { return f() }

// Server 管理只读 handler 和被接管的 WebSocket；停机时必须调用 Close，因为
// net/http 的 Server.Shutdown 不会关闭已接管连接。
type Server struct {
	provider Provider
	interval time.Duration

	mu      sync.Mutex
	clients map[net.Conn]struct{}
}

func New(provider Provider, interval time.Duration) (*Server, error) {
	if provider == nil {
		return nil, ErrNilProvider
	}
	if interval <= 0 {
		interval = time.Second
	}
	return &Server{provider: provider, interval: interval, clients: make(map[net.Conn]struct{})}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/rooms", s.rooms)
	mux.HandleFunc("GET /api/director/recent", s.director)
	mux.HandleFunc("GET /ws", s.websocket)
	return securityHeaders(mux)
}

func (s *Server) Close() error {
	s.mu.Lock()
	clients := make([]net.Conn, 0, len(s.clients))
	for connection := range s.clients {
		clients = append(clients, connection)
	}
	s.mu.Unlock()
	var closeErr error
	for _, connection := range clients {
		closeErr = errors.Join(closeErr, connection.Close())
	}
	return closeErr
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.provider.Snapshot())
}

func (s *Server) rooms(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.provider.Snapshot()
	writeJSON(w, http.StatusOK, struct {
		GeneratedAt time.Time    `json:"generated_at"`
		Rooms       []RoomStatus `json:"rooms"`
	}{snapshot.GeneratedAt, snapshot.Rooms})
}

func (s *Server) director(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.provider.Snapshot()
	writeJSON(w, http.StatusOK, struct {
		GeneratedAt time.Time          `json:"generated_at"`
		Decisions   []DirectorDecision `json:"decisions"`
	}{snapshot.GeneratedAt, snapshot.RecentDirectorDecisions})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	key, err := validateWebSocketRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket upgrade is not supported", http.StatusInternalServerError)
		return
	}
	connection, buffer, err := hijacker.Hijack()
	if err != nil {
		return
	}
	acceptBytes := sha1.Sum([]byte(key + websocketGUID))
	accept := base64.StdEncoding.EncodeToString(acceptBytes[:])
	if _, err := fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		connection.Close()
		return
	}
	if err := buffer.Flush(); err != nil {
		connection.Close()
		return
	}

	s.mu.Lock()
	s.clients[connection] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, connection)
		s.mu.Unlock()
		connection.Close()
	}()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		payload, err := json.Marshal(s.provider.Snapshot())
		if err != nil {
			return
		}
		if err := connection.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
			return
		}
		if err := writeTextFrame(connection, payload); err != nil {
			return
		}
		<-ticker.C
	}
}

func validateWebSocketRequest(r *http.Request) (string, error) {
	if !headerContainsToken(r.Header, "Connection", "upgrade") || !headerContainsToken(r.Header, "Upgrade", "websocket") {
		return "", errors.New("websocket upgrade headers are required")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return "", errors.New("websocket version 13 is required")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		return "", errors.New("invalid websocket key")
	}
	if !allowedOrigin(r.Header.Get("Origin")) {
		return "", errors.New("websocket origin is not allowed")
	}
	return key, nil
}

func headerContainsToken(header http.Header, name, target string) bool {
	for _, value := range header.Values(name) {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), target) {
				return true
			}
		}
	}
	return false
}

// 浏览器客户端只允许回环来源；空 Origin 仍可用于命令行诊断，服务本身也按配置绑定回环地址。
func allowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func writeTextFrame(writer io.Writer, payload []byte) error {
	header := []byte{0x81}
	switch length := len(payload); {
	case length <= 125:
		header = append(header, byte(length))
	case length <= 65535:
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(length))
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(length))
	}
	if _, err := writer.Write(header); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}
