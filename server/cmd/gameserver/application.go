package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/admin"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/bootstrap"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/convert"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/director"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/reward"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/lobby"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/metrics"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/network"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/persistence"
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
	room       *room.Room
	matchID    string
	createdAt  time.Time
	snapshots  *router.SnapshotDispatcher
	events     *router.EventDispatcher
	close      *router.CloseWatcher
	rematching bool // guarded by gameApplication.mu; coalesces repeated clicks

	// The application owns progression between B's authoritative Room phases.
	// These fields are guarded by gameApplication.mu. The stage number makes
	// StageCleared handling idempotent if a reliable batch is ever replayed.
	rewardStage      uint32
	rewarded         map[entity.ID]bool
	delivered        map[entity.ID]bool
	ready            map[entity.ID]bool
	advancing        bool
	completed        bool
	resultSubmitting bool
	resultQueued     bool

	monsters    int
	projectiles map[entity.ID]struct{}
	stats       room.Stats
}

// resumeTokenRepository is D's storage boundary for A4. The application owns
// Session/connection validation; the repository owns atomic token semantics.
type resumeTokenRepository interface {
	IssueRoute(context.Context, string, persistence.ResumeRoute) error
	ConsumeRoute(context.Context, string) (persistence.ResumeRoute, error)
	Revoke(context.Context, string) error
}

// gameApplication assembles A's transport/session boundary, B's Room, and
// D's matchmaking and metrics modules. It is intentionally small: the first
// milestone needs one in-process FIFO queue and two-player rooms.
type gameApplication struct {
	ctx         context.Context
	ids         *idAllocator
	logger      *slog.Logger
	matcher     *lobby.Matchmaker
	metrics     *metrics.Metrics
	roomConfig  room.Config
	gameplay    bootstrap.Gameplay
	startedAt   time.Time
	environment string

	mu             sync.Mutex
	connections    map[*network.Connection]*session.Session
	waiting        map[lobby.PlayerID]*participant
	rooms          map[room.ID]*activeRoom
	nextRoomID     atomic.Uint64
	recentDirector []admin.DirectorDecision
	resumeTokens   resumeTokenRepository
	resumeGrace    time.Duration
	resultWriter   interface {
		Submit(persistence.ResultEnvelope) error
	}
	// Allows deterministic failure of the authoritative opening-stage receipt
	// in network regressions; production uses the room's real control queue.
	openingStageStarter func(*room.Room, stage.Plan) error
}

func newGameApplication(ctx context.Context, logger *slog.Logger, m *metrics.Metrics) (*gameApplication, error) {
	return buildGameApplication(ctx, logger, m, bootstrap.Gameplay{})
}

// newConfiguredGameApplication is the production constructor. The legacy
// constructor remains for focused A-side tests that do not enter reward or
// Director phases; production cannot start with an unvalidated zero value.
func newConfiguredGameApplication(ctx context.Context, logger *slog.Logger, m *metrics.Metrics, gameplay bootstrap.Gameplay) (*gameApplication, error) {
	if !gameplay.Valid() {
		return nil, errors.New("gameserver: invalid gameplay configuration")
	}
	return buildGameApplication(ctx, logger, m, gameplay)
}

func buildGameApplication(ctx context.Context, logger *slog.Logger, m *metrics.Metrics, gameplay bootstrap.Gameplay) (*gameApplication, error) {
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
		gameplay:    gameplay,
		startedAt:   time.Now(),
		environment: "development",
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
	case pb.MessageType_MSG_REWARD_CHOICE:
		return a.handleRewardChoice(c, h, payload, sess)
	case pb.MessageType_MSG_NEXT_STAGE_REQUEST:
		return a.handleNextStageRequest(payload, sess)
	default:
		return nil
	}
}

func (a *gameApplication) handleMatchRequest(c *network.Connection, h network.Header, payload []byte, sess *session.Session) error {
	var request pb.MatchRequest
	if err := proto.Unmarshal(payload, &request); err != nil {
		return err
	}
	if sess.State() == session.StateInRoom {
		return a.handleRematchRequest(sess, h.Sequence)
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
	if sess.State() == session.StateReward {
		return nil // safe discard of combat input already in flight at phase change
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

func (a *gameApplication) handleRewardChoice(c *network.Connection, h network.Header, payload []byte, sess *session.Session) error {
	var choice pb.RewardChoice
	if err := proto.Unmarshal(payload, &choice); err != nil {
		return err
	}
	a.mu.Lock()
	roomID := room.ID(sess.RoomID())
	active := a.rooms[roomID]
	a.mu.Unlock()
	if active == nil {
		return room.ErrClosed
	}
	sessionID, playerID := sess.Identity()
	receipt, err := active.room.ChooseReward(room.SessionID(sessionID), equipment.ID(choice.EquipmentId))
	if err != nil {
		a.rejectRewardChoice(c, h, roomID, choice.EquipmentId, err)
		return nil
	}
	// Room receipts arrive on its next fixed tick, so never block the socket's
	// reader goroutine while the authoritative choice is validated and applied.
	go func() {
		if result := <-receipt; result != nil {
			if errors.Is(result, reward.ErrChoiceAlreadyMade) || errors.Is(result, game.ErrRewardState) {
				a.waitRewardAppliedDelivery(roomID, entity.ID(playerID), c)
			}
			a.rejectRewardChoice(c, h, roomID, choice.EquipmentId, result)
		}
	}()
	return nil
}

// A valid choice and a duplicate can be applied by Room in one tick. The
// private authoritative Applied update must enter the connection's reliable
// queue before the duplicate rejection, regardless of goroutine scheduling.
func (a *gameApplication) waitRewardAppliedDelivery(roomID room.ID, playerID entity.ID, c *network.Connection) {
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		a.mu.Lock()
		active := a.rooms[roomID]
		delivered := active == nil || active.delivered[playerID]
		a.mu.Unlock()
		if delivered || c.IsClosed() {
			return
		}
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (a *gameApplication) rejectRewardChoice(c *network.Connection, h network.Header, roomID room.ID, equipmentID uint32, cause error) {
	a.recordInvalidRewardChoice(roomID, cause)
	reason := pb.ReasonCode_REASON_INVALID_STATE
	if errors.Is(cause, room.ErrQueueFull) || errors.Is(cause, room.ErrClosed) {
		reason = pb.ReasonCode_REASON_INTERNAL
	}
	if err := sendMessage(c, h, pb.MessageType_MSG_REWARD_APPLIED,
		&pb.RewardApplied{Reason: reason, EquipmentId: equipmentID}); err != nil {
		c.Close()
	}
}

func (a *gameApplication) handleNextStageRequest(payload []byte, sess *session.Session) error {
	var request pb.NextStageRequest
	if err := proto.Unmarshal(payload, &request); err != nil {
		return err
	}
	_, playerID := sess.Identity()
	roomID := room.ID(sess.RoomID())
	a.mu.Lock()
	active := a.rooms[roomID]
	if active == nil || active.completed || !active.rewarded[entity.ID(playerID)] {
		a.mu.Unlock()
		return nil
	}
	active.ready[entity.ID(playerID)] = true
	a.mu.Unlock()
	a.tryAdvanceStage(roomID)
	return nil
}

func (a *gameApplication) createMatch(players []*participant, sequence uint32) {
	roomID, active, err := a.newActiveRoom()
	if err != nil {
		a.failMatch(players, err)
		return
	}
	rm := active.room

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

	pendingEvents := make(map[entity.ID]*deferredEventSink, len(players))
	for _, p := range players {
		_, playerID := p.session.Identity()
		active.snapshots.Subscribe(entity.ID(playerID), p.conn)
		reliable := closingSink{connection: p.conn}
		pendingEvents[entity.ID(playerID)] = newDeferredEventSink(reliable)
		active.events.Subscribe(entity.ID(playerID), pendingEvents[entity.ID(playerID)])
		active.close.Subscribe(entity.ID(playerID), pendingEvents[entity.ID(playerID)])
	}
	seed := a.firstStageSeed(roomID)
	firstStage, err := game.NewFirstStagePlan(a.roomConfig.World, seed)
	if err != nil {
		a.logger.Error("first-stage plan failed", "room_id", roomID, "err", err)
		detachPendingMatch(active, players)
		a.failMatch(players, err)
		rm.Close()
		return
	}
	startStage := startOpeningStage
	if a.openingStageStarter != nil {
		startStage = a.openingStageStarter
	}
	if err := startStage(rm, firstStage); err != nil {
		a.logger.Error("start stage failed", "room_id", roomID, "err", err)
		detachPendingMatch(active, players)
		a.failMatch(players, err)
		rm.Close()
		return
	}
	teammates := make([]uint64, 0, len(players)-1)
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
	for _, p := range players {
		_, playerID := p.session.Identity()
		pendingEvents[entity.ID(playerID)].Release()
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

func detachPendingMatch(active *activeRoom, players []*participant) {
	for _, p := range players {
		_, playerID := p.session.Identity()
		active.snapshots.Unsubscribe(entity.ID(playerID))
		active.events.Unsubscribe(entity.ID(playerID))
		active.close.Unsubscribe(entity.ID(playerID))
	}
}

func (a *gameApplication) firstStageSeed(roomID room.ID) int64 {
	if a.gameplay.Valid() {
		return a.gameplay.FirstStageSeed(uint64(roomID))
	}
	return int64(roomID)
}

// MatchRequest in a terminal room is a team replay. Only the server chooses the
// fresh room, seed and team; a client cannot reset a live encounter or choose
// its own teammates. A single request moves both connected participants.
func (a *gameApplication) handleRematchRequest(sess *session.Session, sequence uint32) error {
	a.mu.Lock()
	oldID := room.ID(sess.RoomID())
	old := a.rooms[oldID]
	if old == nil || old.rematching {
		a.mu.Unlock()
		return nil // duplicate or an expired room
	}
	phase := old.room.LatestSnapshot().Stage.State
	if phase != stage.Failed && !(phase == stage.StageClear && old.completed) {
		a.mu.Unlock()
		return nil // replay is forbidden during a live or unfinished stage
	}
	players := make([]*participant, 0, 2)
	for conn, member := range a.connections {
		if member.RoomID() == uint64(oldID) && member.State() == session.StateInRoom && !conn.IsClosed() {
			players = append(players, &participant{conn: conn, session: member})
		}
	}
	if len(players) != 2 { // the existing matchmaker requires a connected pair
		a.mu.Unlock()
		a.logger.Warn("rematch needs two connected players", "room_id", oldID, "connected", len(players))
		return nil
	}
	old.rematching = true
	a.mu.Unlock()
	go a.createRematch(oldID, old, players, sequence)
	return nil
}

func (a *gameApplication) createRematch(oldID room.ID, old *activeRoom, players []*participant, sequence uint32) {
	succeeded := false
	defer func() {
		if !succeeded {
			a.mu.Lock()
			old.rematching = false
			a.mu.Unlock()
		}
	}()
	newID, next, err := a.newActiveRoom()
	if err != nil {
		a.logger.Error("rematch room failed", "room_id", oldID, "err", err)
		return
	}
	newRoom := next.room
	defer func() {
		if !succeeded {
			newRoom.Close()
		}
	}()
	plan, err := game.NewFirstStagePlan(a.roomConfig.World, a.firstStageSeed(newID))
	if err != nil {
		a.logger.Error("rematch plan failed", "room_id", oldID, "err", err)
		return
	}
	// Join before changing any live session binding. Failure leaves all players
	// in the old room and closes the unused new room.
	for _, p := range players {
		sessionID, playerID := p.session.Identity()
		receipt, joinErr := newRoom.Join(room.SessionID(sessionID), entity.ID(playerID))
		if joinErr == nil {
			joinErr = <-receipt
		}
		if joinErr != nil {
			a.logger.Error("rematch join failed", "room_id", newID, "err", joinErr)
			return
		}
	}
	// StageStarted is a reliable event. Hold the new room's events until its
	// StartStage receipt succeeds and MatchFound has entered each connection's
	// reliable queue; otherwise the Bot can discard the start before matching.
	pendingEvents := make(map[entity.ID]*deferredEventSink, len(players))
	for _, p := range players {
		_, playerID := p.session.Identity()
		sink := newDeferredEventSink(closingSink{connection: p.conn})
		pendingEvents[entity.ID(playerID)] = sink
		next.events.Subscribe(entity.ID(playerID), sink)
	}
	startStage := startOpeningStage
	if a.openingStageStarter != nil {
		startStage = a.openingStageStarter
	}
	if err := startStage(newRoom, plan); err != nil {
		a.logger.Error("rematch stage failed", "room_id", newID, "err", err)
		return // old room and player bindings are still intact
	}

	a.mu.Lock()
	if a.rooms[oldID] != old || a.rooms[newID] != next {
		a.mu.Unlock()
		return
	}
	for _, p := range players {
		if a.connections[p.conn] != p.session || p.conn.IsClosed() ||
			p.session.State() != session.StateInRoom || p.session.RoomID() != uint64(oldID) {
			a.mu.Unlock()
			return
		}
	}
	for _, p := range players {
		_, playerID := p.session.Identity()
		old.snapshots.Unsubscribe(entity.ID(playerID))
		old.events.Unsubscribe(entity.ID(playerID))
		old.close.Unsubscribe(entity.ID(playerID))
		p.session.BindRoom(uint64(newID))
		next.snapshots.Subscribe(entity.ID(playerID), p.conn)
		next.close.Subscribe(entity.ID(playerID), closingSink{connection: p.conn})
	}
	a.publishMetricsLocked()
	a.mu.Unlock()

	for _, p := range players {
		_, selfID := p.session.Identity()
		teammates := make([]uint64, 0, len(players)-1)
		for _, other := range players {
			_, otherID := other.session.Identity()
			if otherID != selfID {
				teammates = append(teammates, otherID)
			}
		}
		if err := sendMessage(p.conn, network.Header{Sequence: sequence}, pb.MessageType_MSG_MATCH_FOUND,
			&pb.MatchFound{RoomId: uint64(newID), RoomToken: fmt.Sprintf("room-%d", newID), Teammates: teammates}); err != nil {
			p.conn.Close() // same reliable-delivery contract as first matchmaking
		}
	}
	for _, p := range players {
		_, playerID := p.session.Identity()
		pendingEvents[entity.ID(playerID)].Release()
	}
	for _, p := range players {
		sessionID, _ := p.session.Identity()
		go a.leaveFormerRoom(old.room, room.SessionID(sessionID))
	}
	succeeded = true
	a.logger.Info("rematch ready", "old_room", oldID, "new_room", newID, "players", len(players))
}

func startOpeningStage(rm *room.Room, plan stage.Plan) error {
	for attempt := 0; attempt < 10; attempt++ {
		receipt, err := rm.StartStage(plan)
		if errors.Is(err, room.ErrQueueFull) {
			select {
			case <-rm.Done():
				return room.ErrClosed
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		if err != nil {
			return err
		}
		select {
		case result := <-receipt:
			return result
		case <-rm.Done():
			return room.ErrClosed
		}
	}
	return room.ErrQueueFull
}

// Leaving the former room must not clear the session's *new* binding.
func (a *gameApplication) leaveFormerRoom(rm *room.Room, sessionID room.SessionID) {
	for {
		receipt, err := rm.Leave(sessionID)
		if err == nil {
			err = <-receipt
		}
		if err == nil || errors.Is(err, room.ErrClosed) {
			return
		}
		if !errors.Is(err, room.ErrQueueFull) {
			a.logger.Warn("former room leave failed", "err", err)
			return
		}
		select {
		case <-rm.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// newActiveRoom wires each dispatcher exactly once, for both first matches and
// rematches. The old room stays alive until its former members leave.
func (a *gameApplication) newActiveRoom() (room.ID, *activeRoom, error) {
	matchID, err := newMatchID()
	if err != nil {
		return 0, nil, err
	}
	roomID := room.ID(a.nextRoomID.Add(1))
	rm, err := room.Start(a.ctx, roomID, a.roomConfig, a.gameplay.Catalog())
	if err != nil {
		return 0, nil, err
	}
	active := &activeRoom{
		room:        rm,
		matchID:     matchID,
		createdAt:   time.Now().UTC(),
		snapshots:   router.NewSnapshotDispatcher(),
		events:      router.NewEventDispatcher(),
		close:       router.NewCloseWatcher(),
		projectiles: make(map[entity.ID]struct{}),
		rewarded:    make(map[entity.ID]bool),
		delivered:   make(map[entity.ID]bool),
		ready:       make(map[entity.ID]bool),
	}
	// Route dispatcher saturation warnings through the application logger so
	// reliable-queue overflow is observable alongside other server logs (T10).
	active.events.SetLogger(a.logger)
	active.close.SetLogger(a.logger)
	active.close.OnClose(func(id room.ID, reason string) {
		a.recordRoomStats(id, rm.Stats())
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
	go a.observeSnapshots(roomID, rm, active.snapshots)
	go a.observeEvents(roomID, rm, active.events)
	go a.observeRewards(roomID, rm)
	go active.close.Run(rm)
	go a.observeTicks(roomID, rm)
	return roomID, active, nil
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
	roomID := room.ID(sess.RoomID())
	key := lobby.PlayerID(strconv.FormatUint(playerID, 10))
	a.matcher.Cancel(key)
	delete(a.waiting, key)
	active := a.rooms[roomID]
	lastConnected := true
	if active != nil {
		for _, other := range a.connections {
			if other.RoomID() == sess.RoomID() {
				lastConnected = false
				break
			}
		}
	}
	if active != nil {
		active.snapshots.Unsubscribe(entity.ID(playerID))
		active.events.Unsubscribe(entity.ID(playerID))
		active.close.Unsubscribe(entity.ID(playerID))
	}
	sess.Transition(session.StateDisconnected)
	a.publishMetricsLocked()
	a.mu.Unlock()
	if active != nil {
		if lastConnected {
			// Detach the last player's result before Leave, but do not keep the
			// room alive while a full persistence queue waits for admission.
			go a.submitGameResultAfterCapture(roomID, game.GameAbandoned,
				func() { go a.leaveRoom(sess, active.room) })
		} else {
			go a.leaveRoom(sess, active.room)
		}
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

func (a *gameApplication) observeTicks(roomID room.ID, rm *room.Room) {
	for sample := range rm.TickSamples() {
		a.metrics.ObserveTickWork(sample.WorkDuration)
		a.recordRoomStats(roomID, rm.Stats())
	}
}

func (a *gameApplication) recordRoomStats(roomID room.ID, current room.Stats) {
	var delta metrics.QueueDelta
	a.mu.Lock()
	active := a.rooms[roomID]
	if active == nil {
		a.mu.Unlock()
		return
	}
	delta.RoomRejections = counterDelta(current.QueueRejections, active.stats.QueueRejections)
	delta.RejectedInputs = counterDelta(current.RejectedInputs, active.stats.RejectedInputs)
	delta.DroppedSnapshots = counterDelta(current.DroppedSnapshots, active.stats.DroppedSnapshots)
	delta.DroppedTickSamples = counterDelta(current.DroppedTickSamples, active.stats.DroppedTickSamples)
	active.stats = current
	a.publishQueueMetricsLocked()
	a.mu.Unlock()
	a.metrics.ObserveQueueDelta(delta)
}

func counterDelta(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return current
}

// observeSnapshots remains the room's single snapshot consumer. It records
// authoritative entity counts before delegating network fan-out to A's router.
func (a *gameApplication) observeSnapshots(roomID room.ID, rm *room.Room, dispatcher *router.SnapshotDispatcher) {
	for snapshot := range rm.Snapshots() {
		a.recordSnapshotMetrics(roomID, snapshot)
		dispatcher.Dispatch(snapshot)
	}
}

func (a *gameApplication) recordSnapshotMetrics(roomID room.ID, snapshot room.Snapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	active := a.rooms[roomID]
	if active == nil {
		return
	}
	active.monsters = len(snapshot.Monsters)
	a.publishCombatMetricsLocked()
}

// observeEvents remains the room's single reliable-event consumer. Metrics
// observation is non-blocking with respect to Room Tick and preserves the
// original event batch for A's dispatcher.
func (a *gameApplication) observeEvents(roomID room.ID, rm *room.Room, dispatcher *router.EventDispatcher) {
	for batch := range rm.Events() {
		a.recordEventMetrics(roomID, batch)
		dispatcher.Dispatch(batch)
		for _, event := range batch.Events {
			switch event.Kind {
			case game.StageCleared:
				go a.beginRewardStage(roomID, event.StageIndex)
			case game.TeamDefeated:
				go a.submitGameResult(roomID, game.GameDefeat)
			}
		}
	}
}

// beginRewardStage bridges a reliable StageCleared event into B's existing
// reward state machine. The event observer never waits for Room receipts.
func (a *gameApplication) beginRewardStage(roomID room.ID, stageIndex uint32) {
	if !a.gameplay.Valid() {
		return // focused legacy tests intentionally construct no gameplay bundle
	}
	a.mu.Lock()
	active := a.rooms[roomID]
	if active == nil || active.completed || active.rewardStage >= stageIndex {
		a.mu.Unlock()
		return
	}
	if stageIndex >= a.gameplay.StageLimit() {
		active.completed = true
		a.mu.Unlock()
		go a.submitGameResult(roomID, game.GameVictory)
		a.logger.Info("expedition complete", "room_id", roomID, "stage", stageIndex)
		return
	}
	active.rewardStage = stageIndex
	active.rewarded = make(map[entity.ID]bool)
	active.delivered = make(map[entity.ID]bool)
	active.ready = make(map[entity.ID]bool)
	a.mu.Unlock()

	completed, err := waitCompletedStage(active.room)
	if err != nil {
		a.rewardStartFailed(roomID, stageIndex, err)
		return
	}
	receipt, err := submitStartReward(active.room, a.gameplay.Catalog(), completed.Plan.Seed,
		a.gameplay.RewardDurationTicks())
	if err == nil {
		err = <-receipt
	}
	if err != nil {
		a.rewardStartFailed(roomID, stageIndex, err)
		return
	}
	a.transitionRoomSessions(roomID, session.StateInRoom, session.StateReward)
	a.logger.Info("reward phase started", "room_id", roomID, "stage", stageIndex)
}

func waitCompletedStage(rm *room.Room) (game.StageResult, error) {
	for attempt := 0; attempt < 10; attempt++ {
		receipt, err := rm.CompletedStage()
		if errors.Is(err, room.ErrQueueFull) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if err != nil {
			return game.StageResult{}, err
		}
		result := <-receipt
		return result.Result, result.Err
	}
	return game.StageResult{}, room.ErrQueueFull
}

func submitStartReward(rm *room.Room, catalog equipment.Catalog, seed int64, duration uint64) (<-chan error, error) {
	for attempt := 0; attempt < 10; attempt++ {
		receipt, err := rm.StartReward(catalog, seed, duration)
		if errors.Is(err, room.ErrQueueFull) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		return receipt, err
	}
	return nil, room.ErrQueueFull
}

func (a *gameApplication) rewardStartFailed(roomID room.ID, stageIndex uint32, cause error) {
	a.mu.Lock()
	if active := a.rooms[roomID]; active != nil && active.rewardStage == stageIndex {
		active.rewardStage = 0
	}
	a.mu.Unlock()
	a.logger.Error("start reward phase failed", "room_id", roomID, "stage", stageIndex, "err", cause)
}

func (a *gameApplication) transitionRoomSessions(roomID room.ID, from, to session.State) {
	a.mu.Lock()
	members := make([]*session.Session, 0, 2)
	for connection, member := range a.connections {
		if !connection.IsClosed() && member.RoomID() == uint64(roomID) && member.State() == from {
			members = append(members, member)
		}
	}
	a.mu.Unlock()
	for _, member := range members {
		if !member.Transition(to) {
			a.logger.Warn("session phase transition refused", "room_id", roomID,
				"from", from.String(), "to", to.String())
		}
	}
}

// observeRewards is the Room RewardUpdates channel's single consumer. Each
// option list and result is private to the addressed player.
func (a *gameApplication) observeRewards(roomID room.ID, rm *room.Room) {
	for batch := range rm.RewardUpdates() {
		a.recordRewardMetrics(roomID, batch)
		for _, update := range batch.Updates {
			connection, member := a.rewardRecipient(roomID, update.PlayerID)
			if update.Kind == game.RewardSelectionApplied {
				a.mu.Lock()
				if active := a.rooms[roomID]; active != nil {
					active.rewarded[update.PlayerID] = true
				}
				a.mu.Unlock()
			}
			if connection == nil || member == nil {
				continue
			}
			if member.State() == session.StateInRoom {
				member.Transition(session.StateReward)
			}
			var err error
			switch update.Kind {
			case game.RewardOptionsAvailable:
				ids := make([]uint32, len(update.EquipmentIDs))
				for index, id := range update.EquipmentIDs {
					ids[index] = uint32(id)
				}
				err = sendMessage(connection, network.Header{}, pb.MessageType_MSG_REWARD_OPTIONS,
					&pb.RewardOptions{StageIndex: update.StageIndex, EquipmentIds: ids,
						DeadlineServerTick: update.DeadlineTick})
			case game.RewardSelectionApplied:
				err = sendMessage(connection, network.Header{}, pb.MessageType_MSG_REWARD_APPLIED,
					&pb.RewardApplied{Reason: pb.ReasonCode_REASON_OK, EquipmentId: uint32(update.EquipmentID)})
			default:
				a.logger.Warn("unknown reward update", "room_id", roomID, "kind", update.Kind)
			}
			if err != nil {
				connection.Close()
			} else if update.Kind == game.RewardSelectionApplied {
				a.mu.Lock()
				if active := a.rooms[roomID]; active != nil {
					active.delivered[update.PlayerID] = true
				}
				a.mu.Unlock()
			}
		}
	}
}

func (a *gameApplication) rewardRecipient(roomID room.ID, playerID entity.ID) (*network.Connection, *session.Session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for connection, member := range a.connections {
		_, candidate := member.Identity()
		if !connection.IsClosed() && member.RoomID() == uint64(roomID) && entity.ID(candidate) == playerID {
			return connection, member
		}
	}
	return nil, nil
}

// tryAdvanceStage opens the Director gate only after every connected room
// member has an applied reward and has explicitly pressed ready.
func (a *gameApplication) tryAdvanceStage(roomID room.ID) {
	a.mu.Lock()
	active := a.rooms[roomID]
	if active == nil || active.completed || active.advancing || active.rewardStage == 0 {
		a.mu.Unlock()
		return
	}
	members := 0
	for connection, member := range a.connections {
		if connection.IsClosed() || member.RoomID() != uint64(roomID) || member.State() != session.StateReward {
			continue
		}
		_, playerID := member.Identity()
		members++
		if !active.rewarded[entity.ID(playerID)] || !active.ready[entity.ID(playerID)] {
			a.mu.Unlock()
			return
		}
	}
	if members == 0 {
		a.mu.Unlock()
		return
	}
	active.advancing = true
	a.mu.Unlock()
	go a.advanceStage(roomID, active)
}

func (a *gameApplication) advanceStage(roomID room.ID, active *activeRoom) {
	completed, err := waitCompletedStage(active.room)
	if err != nil {
		a.advanceStageFailed(roomID, active, err)
		return
	}
	started := time.Now()
	plan, decision, err := a.gameplay.Director().Decide(completed.Plan, completed.Performance)
	duration := time.Since(started)
	if err != nil {
		a.advanceStageFailed(roomID, active, err)
		return
	}
	// PreparingNextStage may become visible one tick after the final private
	// reward update. Retry that short authoritative hand-off without generating
	// a second Director decision.
	for attempt := 0; attempt < 30; attempt++ {
		receipt, startErr := active.room.StartStage(plan)
		if errors.Is(startErr, room.ErrQueueFull) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if startErr == nil {
			startErr = <-receipt
		}
		if errors.Is(startErr, game.ErrStageState) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if startErr != nil {
			a.advanceStageFailed(roomID, active, startErr)
			return
		}
		a.transitionRoomSessions(roomID, session.StateReward, session.StateInRoom)
		a.mu.Lock()
		if current := a.rooms[roomID]; current == active {
			active.advancing = false
			active.rewarded = make(map[entity.ID]bool)
			active.delivered = make(map[entity.ID]bool)
			active.ready = make(map[entity.ID]bool)
		}
		a.mu.Unlock()
		if metricErr := a.recordDirectorDecision(roomID, plan.Index, completed.Performance, decision, duration); metricErr != nil {
			a.logger.Warn("director metric rejected", "room_id", roomID, "stage", plan.Index, "err", metricErr)
		}
		a.logger.Info("next stage started", "room_id", roomID, "stage", plan.Index,
			"difficulty", plan.DifficultyScore, "monsters", len(plan.Monsters))
		return
	}
	a.advanceStageFailed(roomID, active, room.ErrQueueFull)
}

func (a *gameApplication) advanceStageFailed(roomID room.ID, expected *activeRoom, cause error) {
	a.mu.Lock()
	if active := a.rooms[roomID]; active == expected {
		active.advancing = false
	}
	a.mu.Unlock()
	a.logger.Error("advance stage failed", "room_id", roomID, "err", cause)
}

func (a *gameApplication) recordEventMetrics(roomID room.ID, batch game.EventBatch) {
	var damage float64
	var cleared, defeated int

	a.mu.Lock()
	active := a.rooms[roomID]
	if active == nil {
		a.mu.Unlock()
		return
	}
	for _, event := range batch.Events {
		switch event.Kind {
		case game.ProjectileSpawned:
			active.projectiles[event.EntityID] = struct{}{}
		case game.ProjectileDestroyed:
			delete(active.projectiles, event.EntityID)
		case game.DamageDealt:
			damage += event.Amount
		case game.StageCleared:
			cleared++
		case game.TeamDefeated:
			defeated++
		}
	}
	a.publishCombatMetricsLocked()
	a.mu.Unlock()

	if damage > 0 {
		if err := a.metrics.ObserveDamage(damage); err != nil {
			a.logger.Warn("invalid authoritative damage metric", "room_id", roomID, "err", err)
		}
	}
	for range cleared {
		_ = a.metrics.ObserveStageResult(metrics.StageResultCleared)
	}
	for range defeated {
		_ = a.metrics.ObserveStageResult(metrics.StageResultDefeated)
	}
}

func (a *gameApplication) publishCombatMetricsLocked() {
	var snapshot metrics.CombatSnapshot
	for _, active := range a.rooms {
		snapshot.ActiveMonsters += active.monsters
		snapshot.ActiveProjectiles += len(active.projectiles)
	}
	_ = a.metrics.SetCombatSnapshot(snapshot)
}

func (a *gameApplication) publishMetricsLocked() {
	_ = a.metrics.SetSnapshot(metrics.Snapshot{
		OnlinePlayers:     len(a.connections),
		ActiveRooms:       len(a.rooms),
		MatchQueuePlayers: a.matcher.Waiting(),
	})
	a.publishCombatMetricsLocked()
	a.publishQueueMetricsLocked()
}

func (a *gameApplication) publishQueueMetricsLocked() {
	var snapshot metrics.QueueSnapshot
	for _, active := range a.rooms {
		snapshot.RoomControlDepth += active.stats.ControlQueueDepth
		snapshot.RoomInputDepth += active.stats.InputQueueDepth
	}
	_ = a.metrics.SetRoomQueueSnapshot(snapshot.RoomControlDepth, snapshot.RoomInputDepth)
}

// recordRewardMetrics is called by A's single RewardUpdates dispatcher after
// it accepts a batch for delivery. It never consumes the Room channel itself.
func (a *gameApplication) recordRewardMetrics(roomID room.ID, batch game.RewardUpdateBatch) {
	for _, update := range batch.Updates {
		var result metrics.RewardResult
		switch update.Kind {
		case game.RewardOptionsAvailable:
			result = metrics.RewardOffered
		case game.RewardSelectionApplied:
			if update.Defaulted {
				result = metrics.RewardDefaulted
			} else {
				result = metrics.RewardChosen
			}
		default:
			a.logger.Warn("unknown authoritative reward update", "room_id", roomID, "kind", update.Kind)
			continue
		}
		_ = a.metrics.ObserveReward(result)
	}
}

// recordInvalidRewardChoice records a rejection only after Room validation;
// raw client values and error strings are kept out of metric labels.
func (a *gameApplication) recordInvalidRewardChoice(roomID room.ID, cause error) {
	_ = a.metrics.ObserveReward(metrics.RewardInvalid)
	a.logger.Info("reward choice rejected", "room_id", roomID, "err", cause)
}

// recordDirectorDecision must be called only after the generated Plan has
// been accepted by Room. That prevents retries or failed plans from appearing
// as applied decisions.
func (a *gameApplication) recordDirectorDecision(roomID room.ID, stageIndex uint32, input director.PerformanceMetrics, output director.Decision, duration time.Duration) error {
	sample := metrics.DirectorSample{
		Duration: duration, ClearTimeSeconds: input.ClearTimeSeconds, TeamHPPercent: input.TeamHPPercent,
		AverageDPS: input.AverageDPS, DeathCount: input.DeathCount, DamageTaken: input.DamageTaken,
		EquipmentPower: input.EquipmentPower, PreviousDifficulty: output.PreviousDifficulty,
		NewDifficulty: output.NewDifficulty, Adjustment: output.Adjustment, MonsterCount: output.MonsterCount,
	}
	if err := a.metrics.ObserveDirector(sample); err != nil {
		return err
	}
	record := admin.DirectorDecision{
		ObservedAt: time.Now().UTC(), RoomID: uint64(roomID), StageIndex: stageIndex, Seed: output.Seed,
		ClearTimeSeconds: input.ClearTimeSeconds, TeamHPPercent: input.TeamHPPercent, AverageDPS: input.AverageDPS,
		DeathCount: input.DeathCount, DamageTaken: input.DamageTaken, EquipmentPower: input.EquipmentPower,
		PreviousDifficulty: output.PreviousDifficulty, NewDifficulty: output.NewDifficulty, Adjustment: output.Adjustment,
		MonsterCount: output.MonsterCount, DurationMicros: duration.Microseconds(), Reasons: slices.Clone(output.Reasons),
	}
	a.mu.Lock()
	a.recentDirector = append(a.recentDirector, record)
	if len(a.recentDirector) > 32 {
		a.recentDirector = slices.Clone(a.recentDirector[len(a.recentDirector)-32:])
	}
	a.mu.Unlock()
	return nil
}

func (a *gameApplication) setEnvironment(environment string) {
	a.mu.Lock()
	a.environment = environment
	a.mu.Unlock()
}

func (a *gameApplication) setResumeTokenStore(store resumeTokenRepository, grace time.Duration) {
	a.mu.Lock()
	a.resumeTokens = store
	a.resumeGrace = grace
	a.mu.Unlock()
}

func (a *gameApplication) setResultWriter(writer interface {
	Submit(persistence.ResultEnvelope) error
}) {
	a.resultWriter = writer
}

func (a *gameApplication) adminSnapshot() admin.Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now().UTC()
	result := admin.Snapshot{
		GeneratedAt: now, Environment: a.environment, UptimeSeconds: time.Since(a.startedAt).Seconds(),
		OnlinePlayers: len(a.connections), ActiveRooms: len(a.rooms), MatchQueuePlayers: a.matcher.Waiting(),
		Rooms:                   make([]admin.RoomStatus, 0, len(a.rooms)),
		RecentDirectorDecisions: make([]admin.DirectorDecision, len(a.recentDirector)),
	}
	for index, decision := range a.recentDirector {
		decision.Reasons = slices.Clone(decision.Reasons)
		result.RecentDirectorDecisions[index] = decision
	}
	for roomID, active := range a.rooms {
		snapshot := active.room.LatestSnapshot()
		stats := active.room.Stats()
		status := admin.RoomStatus{
			RoomID: uint64(roomID), StageIndex: snapshot.Stage.Index, StageSeed: snapshot.Stage.Seed,
			Phase: stagePhase(snapshot.Stage.State), ServerTick: snapshot.ServerTick, Players: len(snapshot.Players),
			Monsters: len(snapshot.Monsters), Projectiles: len(active.projectiles),
			ControlQueueDepth: stats.ControlQueueDepth, InputQueueDepth: stats.InputQueueDepth,
			QueueRejections: stats.QueueRejections, RejectedInputs: stats.RejectedInputs,
			DroppedSnapshots: stats.DroppedSnapshots, DroppedTickSamples: stats.DroppedTickSamples,
			TickWorkMillis: float64(stats.LastTick.WorkDuration) / float64(time.Millisecond),
		}
		result.ActiveMonsters += status.Monsters
		result.ActiveProjectiles += status.Projectiles
		result.RoomQueues.ControlDepth += status.ControlQueueDepth
		result.RoomQueues.InputDepth += status.InputQueueDepth
		result.RoomQueues.Rejections += status.QueueRejections
		result.RoomQueues.RejectedInputs += status.RejectedInputs
		result.RoomQueues.DroppedSnapshots += status.DroppedSnapshots
		result.RoomQueues.DroppedTickSamples += status.DroppedTickSamples
		result.Rooms = append(result.Rooms, status)
	}
	slices.SortFunc(result.Rooms, func(left, right admin.RoomStatus) int {
		return cmp.Compare(left.RoomID, right.RoomID)
	})
	return result
}

func stagePhase(state stage.State) string {
	switch state {
	case stage.Waiting:
		return "waiting"
	case stage.Playing:
		return "playing"
	case stage.StageClear:
		return "stage_clear"
	case stage.Reward:
		return "reward"
	case stage.PreparingNextStage:
		return "preparing_next_stage"
	case stage.Failed:
		return "failed"
	case stage.Closed:
		return "closed"
	default:
		return "unknown"
	}
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
