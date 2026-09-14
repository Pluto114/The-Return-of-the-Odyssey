package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/convert"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
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
	rewards   *router.RewardDispatcher
	close     *router.CloseWatcher
	sessions  []*session.Session
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
	catalog    equipment.Catalog
	planner    director.RuleBasedPlanner
	registry   *session.Registry
	resumeGrace time.Duration

	mu          sync.Mutex
	connections map[*network.Connection]*session.Session
	waiting     map[lobby.PlayerID]*participant
	rooms       map[room.ID]*activeRoom
	nextRoomID  atomic.Uint64
}

func newGameApplication(ctx context.Context, logger *slog.Logger, m *metrics.Metrics, resumeGrace time.Duration) (*gameApplication, error) {
	matcher, err := lobby.NewMatchmaker(2)
	if err != nil {
		return nil, err
	}
	catalog, err := loadEquipmentCatalog(logger)
	if err != nil {
		// The reward phase cannot start without a valid catalog. A missing or
		// malformed catalog is a startup failure (D6 requires the single
		// versioned config source to fail fast), not a runtime degradation.
		return nil, err
	}
	roomConfig := room.DefaultConfig()
	planner, err := director.NewRuleBasedPlanner(director.DefaultRuleConfig(
		roomConfig.World.Min, roomConfig.World.Max, roomConfig.World.Spawn, roomConfig.World.Combat.MaxMonsters))
	if err != nil {
		return nil, err
	}
	app := &gameApplication{
		ctx:         ctx,
		ids:         &idAllocator{},
		logger:      logger,
		matcher:     matcher,
		metrics:     m,
		roomConfig:  roomConfig,
		catalog:     catalog,
		planner:     planner,
		registry:    session.NewRegistry(resumeGrace),
		resumeGrace: resumeGrace,
		connections: make(map[*network.Connection]*session.Session),
		waiting:     make(map[lobby.PlayerID]*participant),
		rooms:       make(map[room.ID]*activeRoom),
	}
	app.publishMetricsLocked()
	return app, nil
}

// loadEquipmentCatalog reads the single versioned equipment config source. The
// path is relative to the process working directory (server/ when run from the
// repo root). A missing or invalid file fails startup rather than silently
// disabling the reward phase.
func loadEquipmentCatalog(logger *slog.Logger) (equipment.Catalog, error) {
	const defaultPath = "data/equipment/catalog.json"
	f, err := os.Open(defaultPath)
	if err != nil {
		return equipment.Catalog{}, fmt.Errorf("open equipment catalog %s: %w", defaultPath, err)
	}
	defer f.Close()
	catalog, err := equipment.Parse(f)
	if err != nil {
		return equipment.Catalog{}, fmt.Errorf("parse equipment catalog: %w", err)
	}
	logger.Info("equipment catalog loaded", "path", defaultPath, "version", catalog.Version(), "items", len(catalog.IDs()))
	return catalog, nil
}

func (a *gameApplication) handle(c *network.Connection, h network.Header, payload []byte) error {
	mt := pb.MessageType(h.MessageType)

	// Ping is stateless and needs no session context.
	if mt == pb.MessageType_MSG_PING {
		return routeMessage(c, h, payload)
	}

	// Login and resume are the only two messages accepted on a connection that
	// has not yet established a session context; both are handled here because
	// they own the session registry and room bindings.
	if mt == pb.MessageType_MSG_LOGIN_REQUEST {
		return a.handleLogin(c, h, payload)
	}
	if mt == pb.MessageType_MSG_RESUME_REQUEST {
		return a.handleResume(c, h, payload)
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
	case pb.MessageType_MSG_REWARD_CHOICE:
		return a.handleRewardChoice(payload, sess)
	case pb.MessageType_MSG_NEXT_STAGE_REQUEST:
		return a.handleNextStageRequest(payload, sess)
	default:
		return nil
	}
}

// handleLogin performs development-mode login: it allocates a fresh
// session/player identity, issues a single-use resume token through the session
// registry, transitions the session to Lobby, and records the connection so a
// later resume can rebind the same identity to a new connection.
func (a *gameApplication) handleLogin(c *network.Connection, h network.Header, payload []byte) error {
	var req pb.LoginRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		return err
	}

	// Protocol version check: must match the header Version (both = 1).
	if req.ProtocolVersion != 0 && req.ProtocolVersion != uint32(network.VersionV1) {
		resp := &pb.LoginResponse{
			ProtocolVersion: uint32(network.VersionV1),
			Reason:          pb.ReasonCode_REASON_INVALID_VERSION,
			Message:         "unsupported protocol version",
		}
		return sendMessage(c, h, pb.MessageType_MSG_LOGIN_RESPONSE, resp)
	}

	sess := session.New()
	sessionID, playerID := a.ids.next()
	sess.AssignIdentity(sessionID, playerID)
	c.SetContext(sess)

	token, err := a.registry.Issue(sess)
	if err != nil {
		a.logger.Error("resume token issue failed", "session_id", sessionID, "err", err)
		return err
	}
	sess.Transition(session.StateLobby)

	a.mu.Lock()
	a.connections[c] = sess
	a.publishMetricsLocked()
	a.mu.Unlock()

	resp := &pb.LoginResponse{
		ProtocolVersion: uint32(network.VersionV1),
		Reason:          pb.ReasonCode_REASON_OK,
		Message:         "ok",
		SessionId:       sessionID,
		PlayerId:        playerID,
		ResumeToken:     []byte(token),
	}
	return sendMessage(c, h, pb.MessageType_MSG_LOGIN_RESPONSE, resp)
}

// handleResume binds a new connection to a previously disconnected session.
// It consumes the one-time resume token, looks up the original session, and —
// on success — rebinds the connection to that identity, re-subscribes the
// player to the room's dispatch streams, and sends a ResumeResponse followed by
// a full authoritative snapshot (plus the pending reward state if any).
//
// A token that is unknown, already consumed, or expired is rejected without
// touching any room state; an expired/disconnected session is never re-created
// as a second player.
func (a *gameApplication) handleResume(c *network.Connection, h network.Header, payload []byte) error {
	var req pb.ResumeRequest
	if err := proto.Unmarshal(payload, &req); err != nil {
		return err
	}

	// Protocol version must still match on the recovery path.
	if req.ProtocolVersion != 0 && req.ProtocolVersion != uint32(network.VersionV1) {
		resp := &pb.ResumeResponse{Reason: pb.ReasonCode_REASON_INVALID_VERSION, Message: "unsupported protocol version"}
		return sendMessage(c, h, pb.MessageType_MSG_RESUME_RESPONSE, resp)
	}

	sess, err := a.registry.Resolve(string(req.ResumeToken))
	switch {
	case err == nil:
		// success, fall through
	case errors.Is(err, session.ErrTokenExpired):
		resp := &pb.ResumeResponse{Reason: pb.ReasonCode_REASON_RESUME_TOKEN_EXPIRED, Message: "resume token expired"}
		return sendMessage(c, h, pb.MessageType_MSG_RESUME_RESPONSE, resp)
	case errors.Is(err, session.ErrSessionActive):
		resp := &pb.ResumeResponse{Reason: pb.ReasonCode_REASON_INVALID_STATE, Message: "session is still connected"}
		return sendMessage(c, h, pb.MessageType_MSG_RESUME_RESPONSE, resp)
	default:
		resp := &pb.ResumeResponse{Reason: pb.ReasonCode_REASON_RESUME_TOKEN_INVALID, Message: "resume token invalid"}
		return sendMessage(c, h, pb.MessageType_MSG_RESUME_RESPONSE, resp)
	}

	sessionID, playerID := sess.Identity()

	// The room must still hold this player. If the grace window already
	// elapsed and the player was removed, the room is gone for them.
	a.mu.Lock()
	active := a.rooms[room.ID(sess.RoomID())]
	a.mu.Unlock()
	if active == nil {
		resp := &pb.ResumeResponse{Reason: pb.ReasonCode_REASON_ROOM_NOT_FOUND, Message: "room no longer available"}
		return sendMessage(c, h, pb.MessageType_MSG_RESUME_RESPONSE, resp)
	}

	// Rebind: the new connection now owns this session. The old connection was
	// already untracked on its disconnect; the session identity is reused, so
	// the room never sees a second player.
	c.SetContext(sess)

	// Re-subscribe this player to the room's per-player streams, targeting the
	// new connection. Snapshots use the latest-wins sink; events/rewards use a
	// reliable sink.
	reliable := closingSink{connection: c}
	active.snapshots.Subscribe(entity.ID(playerID), c)
	active.events.Subscribe(entity.ID(playerID), reliable)
	active.rewards.Subscribe(entity.ID(playerID), reliable)
	active.close.Subscribe(entity.ID(playerID), reliable)

	// Record the rebinding so the next disconnect of THIS connection unwinds
	// cleanly and so metrics reflect the live player again. Any still-live old
	// connection bound to the same session (a half-open TCP connection that
	// has not yet been observed as closed) is dropped so it can no longer send
	// input as this player.
	a.mu.Lock()
	for oldConn, oldSess := range a.connections {
		if oldSess == sess && oldConn != c {
			delete(a.connections, oldConn)
			oldConn.Close()
		}
	}
	a.connections[c] = sess
	a.publishMetricsLocked()
	a.mu.Unlock()

	resp := &pb.ResumeResponse{
		Reason:    pb.ReasonCode_REASON_OK,
		Message:   "ok",
		SessionId: sessionID,
		PlayerId:  playerID,
	}
	if err := sendMessage(c, h, pb.MessageType_MSG_RESUME_RESPONSE, resp); err != nil {
		return err
	}

	// Deliver a full authoritative snapshot so the client rebuilds its view
	// without replaying any pre-disconnect input. The resume state also carries
	// the pending reward offer/applied transition if the room is mid-reward.
	go a.deliverResumeState(active, sess, playerID)
	return nil
}

// deliverResumeState reads the authoritative resume state from the room owner
// goroutine and sends the full snapshot (and pending reward message) to the
// rebound connection. It runs off the Reader goroutine because the room
// receipt resolves on the next tick.
func (a *gameApplication) deliverResumeState(active *activeRoom, sess *session.Session, playerID uint64) {
	sessionID, _ := sess.Identity()
	receipt, err := active.room.ResumeState(room.SessionID(sessionID))
	if err != nil {
		a.logger.Warn("resume state query failed", "player_id", playerID, "err", err)
		return
	}
	state, ok := <-receipt
	if !ok {
		return
	}
	if state.Err != nil {
		a.logger.Warn("resume state rejected", "player_id", playerID, "err", state.Err)
		return
	}

	// Recover the session state to match the room's current stage so the
	// legality matrix admits the right messages (PLAYER_INPUT vs REWARD_CHOICE).
	switch state.State.Snapshot.Stage.State {
	case stage.Reward:
		sess.Transition(session.StateReward)
	default:
		// Playing, StageClear, PreparingNextStage, Failed, etc.: the player is
		// back in the room and may send input / next-stage readiness.
		sess.Transition(session.StateInRoom)
	}

	// Locate the rebound connection for this session (it was just registered in
	// handleResume) and deliver the snapshot + reward state to it.
	a.mu.Lock()
	var conn *network.Connection
	for c, s := range a.connections {
		if s == sess {
			conn = c
			break
		}
	}
	a.mu.Unlock()
	if conn == nil {
		return
	}

	// Full authoritative snapshot (latest-wins).
	ws := convert.WorldSnapshot(state.State.Snapshot.Snapshot, entity.ID(playerID))
	if body, err := proto.Marshal(ws); err == nil {
		if frame, err := network.EncodeFrame(network.Header{
			Magic:       network.Magic,
			Version:     network.VersionV1,
			MessageType: uint16(pb.MessageType_MSG_WORLD_SNAPSHOT),
		}, body); err == nil {
			conn.SendSnapshot(frame)
		}
	}

	// Pending reward offer/applied transition, if the room is mid-reward.
	if state.State.Reward != nil {
		if mt, msg, err := convert.Reward(*state.State.Reward); err == nil {
			if body, err := proto.Marshal(msg); err == nil {
				if frame, err := network.EncodeFrame(network.Header{
					Magic:       network.Magic,
					Version:     network.VersionV1,
					MessageType: mt,
				}, body); err == nil {
					conn.Send(frame)
				}
			}
		}
	}

	a.logger.Info("resume complete", "player_id", playerID, "room_id", sess.RoomID())
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

// handleRewardChoice routes a client's reward selection to the room. The room
// resolves the player identity from the trusted Session binding and World
// validates the choice against the private offer (non-candidate, duplicate,
// expired, or out-of-state choices are rejected without re-applying modifiers).
func (a *gameApplication) handleRewardChoice(payload []byte, sess *session.Session) error {
	var message pb.RewardChoice
	if err := proto.Unmarshal(payload, &message); err != nil {
		return err
	}
	a.mu.Lock()
	active := a.rooms[room.ID(sess.RoomID())]
	a.mu.Unlock()
	if active == nil {
		return room.ErrClosed
	}
	sessionID, _ := sess.Identity()
	_, err := active.room.ChooseReward(room.SessionID(sessionID), equipment.ID(message.EquipmentId))
	return err
}

// handleNextStageRequest routes a client's "ready for next stage" signal to the
// room's ready barrier. The room resolves the trusted session binding and marks
// the player ready idempotently; duplicate or premature signals cannot advance
// the stage on their own — the server advances only once every bound session is
// ready and the reward round is complete.
func (a *gameApplication) handleNextStageRequest(payload []byte, sess *session.Session) error {
	var message pb.NextStageRequest
	if err := proto.Unmarshal(payload, &message); err != nil {
		return err
	}
	a.mu.Lock()
	active := a.rooms[room.ID(sess.RoomID())]
	a.mu.Unlock()
	if active == nil {
		return room.ErrClosed
	}
	sessionID, _ := sess.Identity()
	_, err := active.room.Ready(room.SessionID(sessionID))
	return err
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
		rewards:   router.NewRewardDispatcher(),
		close:     router.NewCloseWatcher(),
	}
	// Route dispatcher saturation warnings through the application logger so
	// reliable-queue overflow is observable alongside other server logs (T10).
	active.events.SetLogger(a.logger)
	active.rewards.SetLogger(a.logger)
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
	go active.rewards.Run(rm)
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
		active.sessions = append(active.sessions, p.session)
		active.snapshots.Subscribe(entity.ID(playerID), p.conn)
		reliable := closingSink{connection: p.conn}
		active.events.Subscribe(entity.ID(playerID), reliable)
		active.rewards.Subscribe(entity.ID(playerID), reliable)
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

	// Start the opening encounter. The room-level seed is derived from the
	// room ID so every subsequent director stage stays reproducible; the Room
	// copies and validates the plan before enqueueing the trusted command.
	firstStage, err := game.NewFirstStagePlan(a.roomConfig.World, int64(roomID))
	if err != nil {
		a.logger.Error("first-stage plan failed", "room_id", roomID, "err", err)
		a.failMatch(players, err)
		rm.Close()
		return
	}
	if _, err := rm.StartStage(firstStage); err != nil {
		a.logger.Error("start stage failed", "room_id", roomID, "err", err)
		a.failMatch(players, err)
		rm.Close()
		return
	}

	// Orchestrate the full stage lifecycle: on StageClear move the room into
	// Reward, then wait for the next-stage ready barrier before advancing via
	// the Director. This runs the Clear→Reward→Ready→Director→NextStage cycle
	// continuously until the room closes (or the run ends).
	go a.orchestrateStages(rm, roomID, active.sessions)

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
		active.rewards.Unsubscribe(entity.ID(playerID))
		active.close.Unsubscribe(entity.ID(playerID))
	}
	sess.Transition(session.StateDisconnected)
	// Start the reconnect grace window: the resume token becomes consumable
	// and, if it is not used in time, the player is removed from the room.
	a.registry.MarkDisconnected(sess)
	a.publishMetricsLocked()
	a.mu.Unlock()

	// A player who never reached a room has nothing to recover; clear its token
	// immediately so it cannot be resumed into a stale state.
	if active == nil {
		a.registry.Revoke(sess)
		sess.Transition(session.StateClosed)
		return
	}

	// Grace window: keep the room binding (identity/equipment/HP are preserved
	// by the World) and only remove the player once the token expires unused.
	go a.expireSession(sess, active.room)
}

// expireSession waits out the reconnect grace window. If the session has not
// been resumed (its state moved off Disconnected) by then, it removes the
// player from the room and revokes the token so it can never be replayed.
func (a *gameApplication) expireSession(sess *session.Session, rm *room.Room) {
	select {
	case <-a.ctx.Done():
		return
	case <-time.After(a.resumeGrace):
	}
	// A resumed session transitions off StateDisconnected; only still-disconnected
	// sessions are torn down.
	if sess.State() != session.StateDisconnected {
		return
	}
	sessionID, _ := sess.Identity()
	a.registry.Revoke(sess)
	a.leaveRoom(sess, rm)
	sess.Transition(session.StateClosed)
	a.logger.Info("resume grace expired, player removed", "session_id", sessionID)
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

// rewardSelectionTicks is the reward-round choice window in server ticks
// (15s at TickRate=30). Exceeding it makes World apply each player's default
// (first offered) selection.
const rewardSelectionTicks = 450

// orchestrateStages drives the server-owned stage lifecycle for one room:
//
//	Playing -> StageClear -> Reward -> PreparingNextStage -> (all ready) ->
//	Director -> next Playing
//
// It is the single owner of the reward transition and the next-stage ready
// barrier. Clients only ever signal readiness; they never submit a plan,
// difficulty, or monster stats — the Director builds the next stage from the
// previous plan plus the frozen performance metrics, and the Room validates it
// before admitting the trusted command.
//
// Session state is mirrored here: players move InRoom -> Reward when the reward
// round starts and Reward -> InRoom when the next stage begins, so the session
// legality matrix admits REWARD_CHOICE only during Reward and PLAYER_INPUT only
// during InRoom.
func (a *gameApplication) orchestrateStages(rm *room.Room, roomID room.ID, sessions []*session.Session) {
	ticker := time.NewTicker(game.TickInterval)
	defer ticker.Stop()

	rewardStarted := false
	var readiness <-chan room.ReadinessReceipt // nil until a readiness query is in flight
	for {
		select {
		case <-rm.Done():
			return
		case <-ticker.C:
			latest := rm.LatestSnapshot()
			if latest.Closed {
				return
			}

			switch latest.Stage.State {
			case stage.StageClear:
				if rewardStarted {
					continue
				}
				// Reward is a server-owned transition: no client message
				// triggers it. Move sessions into Reward so REWARD_CHOICE is
				// admitted, then start the round.
				transitionSessions(sessions, session.StateReward)
				// Derive a deterministic, per-stage reward seed from the room
				// ID and stage index so the offer set is reproducible without
				// process-global randomness.
				seed := int64(roomID) ^ int64(latest.Stage.Index)*2654435761
				if _, err := rm.StartReward(a.catalog, seed, rewardSelectionTicks); err != nil {
					a.logger.Error("start reward failed", "room_id", roomID, "err", err)
					return
				}
				rewardStarted = true
				a.logger.Info("reward round started", "room_id", roomID, "stage", latest.Stage.Index)

			case stage.PreparingNextStage:
				// Reward is complete; wait for every bound session to signal
				// readiness before advancing. A readiness query is issued at
				// most once at a time, so the control queue is never flooded.
				if readiness != nil {
					continue
				}
				receipt, err := rm.Readiness()
				if err != nil {
					if errors.Is(err, room.ErrQueueFull) {
						continue
					}
					a.logger.Error("readiness query failed", "room_id", roomID, "err", err)
					return
				}
				readiness = receipt
			}

		case got, ok := <-readiness:
			readiness = nil
			if !ok {
				return // room closed while the query was in flight
			}
			if got.Ready < got.Online || got.Online == 0 {
				continue // not everyone ready yet; re-poll next tick
			}
			if err := a.advanceNextStage(rm, roomID, sessions); err != nil {
				a.logger.Error("advance next stage failed", "room_id", roomID, "err", err)
				return
			}
			rewardStarted = false
			transitionSessions(sessions, session.StateInRoom)
		}
	}
}

// advanceNextStage runs once every bound session is ready: it reads the frozen
// completed-stage plan and performance metrics, generates the next stage through
// the Director, and submits the trusted StartStage command. Each receipt resolves
// on the room's owner goroutine (or is drained with ErrClosed on teardown), so
// blocking here is safe and bounded.
func (a *gameApplication) advanceNextStage(rm *room.Room, roomID room.ID, sessions []*session.Session) error {
	completedReceipt, err := rm.CompletedStage()
	if err != nil {
		return err
	}
	completed, ok := <-completedReceipt
	if !ok {
		return errors.New("completed stage receipt closed without a result")
	}
	if completed.Err != nil {
		return completed.Err
	}

	nextPlan, decision, err := a.planner.Decide(completed.Result.Plan, completed.Result.Performance)
	if err != nil {
		return err
	}
	a.logger.Info("director advanced stage", "room_id", roomID,
		"from_stage", completed.Result.Plan.Index, "to_stage", nextPlan.Index,
		"difficulty", decision.NewDifficulty, "monsters", decision.MonsterCount,
		"seed", decision.Seed)

	if _, err := rm.StartStage(nextPlan); err != nil {
		return err
	}
	return nil
}

// transitionSessions moves every live session into the target state, ignoring
// sessions that have already disconnected (whose transition is a no-op or
// terminal).
func transitionSessions(sessions []*session.Session, to session.State) {
	for _, s := range sessions {
		s.Transition(to)
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
