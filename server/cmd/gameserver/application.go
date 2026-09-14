package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/convert"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/lobby"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/room"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/router"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/session"
)

type participant struct {
	conn     *network.Connection
	session  *session.Session
	queuedAt time.Time
}

type activeRoom struct {
	room      *room.Room
	snapshots *router.SnapshotDispatcher
	events    *router.EventDispatcher
	close     *router.CloseWatcher
}

// gameApplication assembles A's transport/session boundary, B's Room, and
// D's matchmaking and metrics modules. It is intentionally small: the first
// milestone needs one in-process FIFO queue and two-player rooms.
type gameApplication struct {
	ctx        context.Context
	ids        *idAllocator
	logger     *slog.Logger
	matcher    *lobby.Matchmaker
	metrics    *metrics.Metrics
	roomConfig room.Config

	mu          sync.Mutex
	connections map[*network.Connection]*session.Session
	waiting     map[lobby.PlayerID]*participant
	rooms       map[room.ID]*activeRoom
	nextRoomID  atomic.Uint64
}

func newGameApplication(ctx context.Context, logger *slog.Logger, m *metrics.Metrics) (*gameApplication, error) {
	matcher, err := lobby.NewMatchmaker(2)
	if err != nil {
		return nil, err
	}
	app := &gameApplication{
		ctx:         ctx,
		ids:         &idAllocator{},
		logger:      logger,
		matcher:     matcher,
		metrics:     m,
		roomConfig:  room.DefaultConfig(),
		connections: make(map[*network.Connection]*session.Session),
		waiting:     make(map[lobby.PlayerID]*participant),
		rooms:       make(map[room.ID]*activeRoom),
	}
	app.publishMetricsLocked()
	return app, nil
}

func (a *gameApplication) handle(c *network.Connection, h network.Header, payload []byte) error {
	mt := pb.MessageType(h.MessageType)
	if mt == pb.MessageType_MSG_PING || mt == pb.MessageType_MSG_LOGIN_REQUEST {
		err := routeMessage(c, h, payload, a.ids, a.logger)
		if err == nil && mt == pb.MessageType_MSG_LOGIN_REQUEST {
			if sess, ok := c.Context().(*session.Session); ok && sess.State() == session.StateLobby {
				a.mu.Lock()
				a.connections[c] = sess
				a.publishMetricsLocked()
				a.mu.Unlock()
			}
		}
		return err
	}

	sess, ok := c.Context().(*session.Session)
	if !ok {
		return sendDisconnect(c, h, pb.ReasonCode_REASON_INVALID_STATE, "login required")
	}
	if accepted, reason := sess.Accept(mt); !accepted {
		return sendDisconnect(c, h, reason, "invalid message for session state")
	}

	switch mt {
	case pb.MessageType_MSG_MATCH_REQUEST:
		return a.handleMatchRequest(c, h, payload, sess)
	case pb.MessageType_MSG_MATCH_CANCEL:
		return a.handleMatchCancel(payload, sess)
	case pb.MessageType_MSG_PLAYER_INPUT:
		return a.handlePlayerInput(payload, sess)
	default:
		return nil
	}
}

func (a *gameApplication) handleMatchRequest(c *network.Connection, h network.Header, payload []byte, sess *session.Session) error {
	var request pb.MatchRequest
	if err := proto.Unmarshal(payload, &request); err != nil {
		return err
	}
	if sess.State() == session.StateMatching {
		return nil // protocol contract: duplicate request while queued is a no-op
	}
	if !sess.Transition(session.StateMatching) {
		return fmt.Errorf("match transition failed")
	}
	_, playerID := sess.Identity()
	key := lobby.PlayerID(strconv.FormatUint(playerID, 10))
	member := &participant{conn: c, session: sess, queuedAt: time.Now()}

	a.mu.Lock()
	a.waiting[key] = member
	match, err := a.matcher.Enqueue(key)
	if err != nil {
		delete(a.waiting, key)
		sess.Transition(session.StateLobby)
		a.publishMetricsLocked()
		a.mu.Unlock()
		return err
	}
	if !match.Found() {
		a.publishMetricsLocked()
		a.mu.Unlock()
		return nil
	}
	players := make([]*participant, 0, len(match.Players))
	for _, id := range match.Players {
		p := a.waiting[id]
		delete(a.waiting, id)
		if p == nil {
			a.mu.Unlock()
			return fmt.Errorf("matched participant %q disappeared", id)
		}
		players = append(players, p)
	}
	a.publishMetricsLocked()
	a.mu.Unlock()

	// Room Join waits for a tick receipt, so it must never block a connection's
	// reader goroutine. MatchFound is sent only after every Join succeeds.
	go a.createMatch(players, h.Sequence)
	return nil
}

func (a *gameApplication) handleMatchCancel(payload []byte, sess *session.Session) error {
	var cancel pb.MatchCancel
	if err := proto.Unmarshal(payload, &cancel); err != nil {
		return err
	}
	if sess.State() != session.StateMatching {
		return nil
	}
	_, playerID := sess.Identity()
	key := lobby.PlayerID(strconv.FormatUint(playerID, 10))
	a.mu.Lock()
	a.matcher.Cancel(key)
	delete(a.waiting, key)
	sess.Transition(session.StateLobby)
	a.publishMetricsLocked()
	a.mu.Unlock()
	return nil
}

func (a *gameApplication) handlePlayerInput(payload []byte, sess *session.Session) error {
	var message pb.PlayerInput
	if err := proto.Unmarshal(payload, &message); err != nil {
		return err
	}
	input, err := convert.Input(&message)
	if err != nil {
		return err
	}
	a.mu.Lock()
	active := a.rooms[room.ID(sess.RoomID())]
	a.mu.Unlock()
	if active == nil {
		return room.ErrClosed
	}
	sessionID, _ := sess.Identity()
	return active.room.Input(room.SessionID(sessionID), input)
}

func (a *gameApplication) createMatch(players []*participant, sequence uint32) {
	roomID := room.ID(a.nextRoomID.Add(1))
	rm, err := room.Start(a.ctx, roomID, a.roomConfig)
	if err != nil {
		a.failMatch(players, err)
		return
	}
	active := &activeRoom{
		room:      rm,
		snapshots: router.NewSnapshotDispatcher(),
		events:    router.NewEventDispatcher(),
		close:     router.NewCloseWatcher(),
	}
	// Route dispatcher saturation warnings through the application logger so
	// reliable-queue overflow is observable alongside other server logs (T10).
	active.events.SetLogger(a.logger)
	active.close.SetLogger(a.logger)
	active.close.OnClose(func(id room.ID, reason string) {
		a.mu.Lock()
		delete(a.rooms, id)
		a.publishMetricsLocked()
		a.mu.Unlock()
		a.logger.Info("room closed", "room_id", id, "reason", reason)
	})
	a.mu.Lock()
	a.rooms[roomID] = active
	a.publishMetricsLocked()
	a.mu.Unlock()
	go active.snapshots.Run(rm)
	go active.events.Run(rm)
	go active.close.Run(rm)
	go a.observeTicks(rm)

	for _, p := range players {
		if p.session.State() != session.StateMatching {
			a.failMatch(players, errors.New("participant disconnected before room join"))
			rm.Close()
			return
		}
		if err := router.Join(p.session, rm, uint64(roomID)); err != nil {
			a.failMatch(players, err)
			rm.Close()
			return
		}
		if p.conn.IsClosed() {
			a.failMatch(players, errors.New("participant disconnected during room join"))
			rm.Close()
			return
		}
	}

	teammates := make([]uint64, 0, len(players)-1)
	for _, p := range players {
		_, playerID := p.session.Identity()
		active.snapshots.Subscribe(entity.ID(playerID), p.conn)
		reliable := closingSink{connection: p.conn}
		active.events.Subscribe(entity.ID(playerID), reliable)
		active.close.Subscribe(entity.ID(playerID), reliable)
	}
	for _, p := range players {
		_, selfID := p.session.Identity()
		teammates = teammates[:0]
		for _, other := range players {
			_, otherID := other.session.Identity()
			if otherID != selfID {
				teammates = append(teammates, otherID)
			}
		}
		err := sendMessage(p.conn, network.Header{Sequence: sequence}, pb.MessageType_MSG_MATCH_FOUND, &pb.MatchFound{
			RoomId:    uint64(roomID),
			RoomToken: fmt.Sprintf("room-%d", roomID),
			Teammates: append([]uint64(nil), teammates...),
		})
		if err != nil {
			p.conn.Close()
		}
	}
	oldest := time.Now()
	for _, p := range players {
		if p.queuedAt.Before(oldest) {
			oldest = p.queuedAt
		}
	}
	_ = a.metrics.ObserveMatch(time.Since(oldest))
	a.logger.Info("match ready", "room_id", roomID, "players", len(players))
}

func (a *gameApplication) failMatch(players []*participant, cause error) {
	a.logger.Error("match assembly failed", "err", cause)
	for _, p := range players {
		_ = sendDisconnect(p.conn, network.Header{}, pb.ReasonCode_REASON_ROOM_CLOSED, cause.Error())
	}
}

func (a *gameApplication) disconnected(c *network.Connection) {
	a.mu.Lock()
	sess := a.connections[c]
	delete(a.connections, c)
	if sess == nil {
		a.publishMetricsLocked()
		a.mu.Unlock()
		return
	}
	_, playerID := sess.Identity()
	key := lobby.PlayerID(strconv.FormatUint(playerID, 10))
	a.matcher.Cancel(key)
	delete(a.waiting, key)
	active := a.rooms[room.ID(sess.RoomID())]
	if active != nil {
		active.snapshots.Unsubscribe(entity.ID(playerID))
		active.events.Unsubscribe(entity.ID(playerID))
		active.close.Unsubscribe(entity.ID(playerID))
	}
	sess.Transition(session.StateDisconnected)
	a.publishMetricsLocked()
	a.mu.Unlock()
	if active != nil {
		go a.leaveRoom(sess, active.room)
	}
}

func (a *gameApplication) leaveRoom(sess *session.Session, rm *room.Room) {
	for {
		err := router.Leave(sess, rm)
		if err == nil || errors.Is(err, room.ErrClosed) {
			return
		}
		if !errors.Is(err, room.ErrQueueFull) {
			a.logger.Warn("room leave failed", "err", err)
			return
		}
		select {
		case <-rm.Done():
			return
		case <-time.After(time.Millisecond * 10):
		}
	}
}

func (a *gameApplication) observeTicks(rm *room.Room) {
	for sample := range rm.TickSamples() {
		a.metrics.ObserveTickWork(sample.WorkDuration)
	}
}

func (a *gameApplication) publishMetricsLocked() {
	_ = a.metrics.SetSnapshot(metrics.Snapshot{
		OnlinePlayers:     len(a.connections),
		ActiveRooms:       len(a.rooms),
		MatchQueuePlayers: a.matcher.Waiting(),
	})
}

// closingSink enforces the reliable-queue contract: saturation closes only
// the slow connection instead of silently discarding a gameplay event.
type closingSink struct{ connection *network.Connection }

func (s closingSink) Send(frame []byte) bool {
	if s.connection.Send(frame) {
		return true
	}
	// Dispatchers hold their subscription read lock while invoking Send.
	// Close asynchronously so the disconnect callback can unsubscribe safely.
	go s.connection.Close()
	return false
}
