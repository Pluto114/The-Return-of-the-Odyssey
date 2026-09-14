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
	room      *room.Room
	snapshots *router.SnapshotDispatcher
	events    *router.EventDispatcher
	close     *router.CloseWatcher

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
		room:        rm,
		snapshots:   router.NewSnapshotDispatcher(),
		events:      router.NewEventDispatcher(),
		close:       router.NewCloseWatcher(),
		projectiles: make(map[entity.ID]struct{}),
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
	go active.close.Run(rm)
	go a.observeTicks(roomID, rm)

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
	}
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
