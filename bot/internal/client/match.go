package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"time"

	pb "github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"google.golang.org/protobuf/proto"
)

type Phase string

const (
	PhaseLogin    Phase = "login"
	PhaseMatch    Phase = "match"
	PhaseCombat   Phase = "combat"
	PhaseReward   Phase = "reward"
	PhaseReady    Phase = "ready"
	PhaseResume   Phase = "resume"
	PhaseComplete Phase = "complete"
)

type RewardScenario string

const (
	RewardNormal    RewardScenario = "normal"
	RewardInvalid   RewardScenario = "invalid"
	RewardDuplicate RewardScenario = "duplicate"
	RewardTimeout   RewardScenario = "timeout"
)

var (
	ErrInvalidMatchConfig = errors.New("invalid bot match configuration")
	ErrInvalidReward      = errors.New("invalid reward lifecycle")
	ErrResumeRejected     = errors.New("resume rejected")
)

type MatchConfig struct {
	TargetStages      uint32
	Seed              uint64
	RewardScenario    RewardScenario
	UsePotion         bool
	EnableResume      bool
	MaxResumeAttempts int
}

func DefaultMatchConfig() MatchConfig {
	return MatchConfig{
		TargetStages:      3,
		Seed:              1,
		RewardScenario:    RewardNormal,
		UsePotion:         true,
		EnableResume:      true,
		MaxResumeAttempts: 1,
	}
}

func (c MatchConfig) validate() error {
	if c.TargetStages < 1 || c.TargetStages > 1000 {
		return fmt.Errorf("%w: target stages must be in [1, 1000]", ErrInvalidMatchConfig)
	}
	switch c.RewardScenario {
	case RewardNormal, RewardInvalid, RewardDuplicate, RewardTimeout:
	default:
		return fmt.Errorf("%w: unknown reward scenario %q", ErrInvalidMatchConfig, c.RewardScenario)
	}
	if c.MaxResumeAttempts < 0 || (c.EnableResume && c.MaxResumeAttempts == 0) {
		return fmt.Errorf("%w: resume attempts must be positive when resume is enabled", ErrInvalidMatchConfig)
	}
	return nil
}

func (c MatchConfig) Validate() error { return c.validate() }

// MatchResult is a bounded diagnostic summary suitable for both functional
// verification and sustained-load aggregation.
type MatchResult struct {
	ClientID          int              `json:"client_id"`
	Phase             Phase            `json:"phase"`
	SessionID         uint64           `json:"session_id"`
	PlayerID          uint64           `json:"player_id"`
	RoomID            uint64           `json:"room_id"`
	StageIndex        uint32           `json:"stage_index"`
	LastServerTick    uint64           `json:"last_server_tick"`
	StagesCleared     uint32           `json:"stages_cleared"`
	RewardsApplied    uint32           `json:"rewards_applied"`
	ReadySent         uint32           `json:"ready_sent"`
	PotionInputs      uint32           `json:"potion_inputs"`
	RecoveryAttempts  uint32           `json:"recovery_attempts"`
	RecoverySucceeded uint32           `json:"recovery_succeeded"`
	DisconnectReason  string           `json:"disconnect_reason,omitempty"`
	ReloadsStarted    uint32           `json:"reloads_started"`
	DirectorSamples   []DirectorSample `json:"director_samples,omitempty"`
}

type DirectorSample struct {
	Stage      uint32  `json:"stage"`
	Difficulty float32 `json:"difficulty"`
	Adjustment float32 `json:"adjustment"`
}

type actionKind uint8

const (
	actionRewardChoice actionKind = iota + 1
	actionReady
)

type outboundAction struct {
	kind        actionKind
	messageType pb.MessageType
	message     proto.Message
}

type lifecycleProgress struct {
	combat   *combatProgress
	config   MatchConfig
	clientID int

	mu sync.Mutex

	phase             Phase
	roomID            uint64
	stagesCleared     uint32
	rewardsApplied    uint32
	readySent         uint32
	potionInputs      uint32
	recoveryAttempts  uint32
	recoverySucceeded uint32
	disconnectReason  string
	rewardStage       uint32
	rewardOptions     []uint32
	validRewardChoice uint32
	rewardResponses   uint32
	invalidRejected   bool
	potionSentStage   uint32
	actions           []outboundAction
}

func newLifecycleProgress(config MatchConfig, clientID int, roomID uint64) *lifecycleProgress {
	return &lifecycleProgress{
		combat:   &combatProgress{aimX: 1},
		config:   config,
		clientID: clientID,
		phase:    PhaseMatch,
		roomID:   roomID,
	}
}

func (p *lifecycleProgress) observe(messageType pb.MessageType, body []byte) error {
	switch messageType {
	case pb.MessageType_MSG_REWARD_OPTIONS:
		return p.observeRewardOptions(body)
	case pb.MessageType_MSG_REWARD_APPLIED:
		return p.observeRewardApplied(body)
	}
	if err := p.combat.observe(messageType, body); err != nil {
		return err
	}

	switch messageType {
	case pb.MessageType_MSG_STAGE_STARTED_EVENT:
		var event pb.StageStartedEvent
		if err := proto.Unmarshal(body, &event); err != nil {
			return err
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if event.StageIndex != p.stagesCleared+1 || event.StageIndex > p.config.TargetStages {
			return fmt.Errorf("stage started out of order: got %d after %d clears", event.StageIndex, p.stagesCleared)
		}
		p.phase = PhaseCombat
		p.rewardStage = 0
		p.rewardOptions = nil
		p.validRewardChoice = 0
		p.rewardResponses = 0
		p.invalidRejected = false
	case pb.MessageType_MSG_STAGE_CLEARED_EVENT:
		var event pb.StageClearedEvent
		if err := proto.Unmarshal(body, &event); err != nil {
			return err
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.phase != PhaseCombat || event.StageIndex != p.stagesCleared+1 {
			return fmt.Errorf("stage cleared out of order: got %d after %d clears in %s", event.StageIndex, p.stagesCleared, p.phase)
		}
		p.stagesCleared++
		if p.stagesCleared >= p.config.TargetStages {
			p.phase = PhaseComplete
		} else {
			p.phase = PhaseReward
		}
	}
	return nil
}

func (p *lifecycleProgress) observeRewardOptions(body []byte) error {
	var options pb.RewardOptions
	if err := proto.Unmarshal(body, &options); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, lastTick := p.combat.position()
	if p.phase != PhaseReward || p.rewardStage != 0 || options.StageIndex != p.stagesCleared || len(options.EquipmentIds) < 1 || len(options.EquipmentIds) > 3 ||
		options.DeadlineServerTick == 0 || options.DeadlineServerTick < lastTick {
		return fmt.Errorf("%w: malformed options for stage %d", ErrInvalidReward, options.StageIndex)
	}
	seen := make(map[uint32]struct{}, len(options.EquipmentIds))
	for _, id := range options.EquipmentIds {
		if id == 0 {
			return fmt.Errorf("%w: zero equipment id", ErrInvalidReward)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%w: duplicate equipment id %d", ErrInvalidReward, id)
		}
		seen[id] = struct{}{}
	}
	p.rewardStage = options.StageIndex
	p.rewardOptions = slices.Clone(options.EquipmentIds)
	p.validRewardChoice = deterministicRewardChoice(p.config.Seed, p.clientID, options.StageIndex, options.EquipmentIds)

	switch p.config.RewardScenario {
	case RewardNormal:
		p.queueRewardChoice(p.validRewardChoice)
	case RewardInvalid:
		p.queueRewardChoice(invalidEquipmentID(seen))
	case RewardDuplicate:
		p.queueRewardChoice(p.validRewardChoice)
		p.queueRewardChoice(p.validRewardChoice)
	case RewardTimeout:
		// Deliberately wait for the server-authored default RewardApplied.
	}
	return nil
}

func (p *lifecycleProgress) observeRewardApplied(body []byte) error {
	var applied pb.RewardApplied
	if err := proto.Unmarshal(body, &applied); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase != PhaseReward || p.rewardStage == 0 || len(p.rewardOptions) == 0 {
		return fmt.Errorf("%w: applied without active options", ErrInvalidReward)
	}
	p.rewardResponses++

	switch p.config.RewardScenario {
	case RewardInvalid:
		if !p.invalidRejected {
			if applied.Reason == pb.ReasonCode_REASON_OK {
				return fmt.Errorf("%w: server accepted an unoffered equipment id", ErrInvalidReward)
			}
			p.invalidRejected = true
			p.queueRewardChoice(p.validRewardChoice)
			return nil
		}
		if err := p.requireAppliedChoice(&applied); err != nil {
			return err
		}
	case RewardDuplicate:
		if p.rewardResponses == 1 {
			if err := p.requireAppliedChoice(&applied); err != nil {
				return err
			}
			return nil
		}
		if p.rewardResponses != 2 || applied.Reason == pb.ReasonCode_REASON_OK {
			return fmt.Errorf("%w: duplicate choice was not explicitly rejected", ErrInvalidReward)
		}
	case RewardNormal:
		if p.rewardResponses != 1 {
			return fmt.Errorf("%w: unexpected extra reward response", ErrInvalidReward)
		}
		if err := p.requireAppliedChoice(&applied); err != nil {
			return err
		}
	case RewardTimeout:
		if p.rewardResponses != 1 || applied.Reason != pb.ReasonCode_REASON_OK || applied.EquipmentId != p.rewardOptions[0] {
			return fmt.Errorf("%w: invalid timeout default response", ErrInvalidReward)
		}
	}

	p.rewardsApplied++
	p.phase = PhaseReady
	p.actions = append(p.actions, outboundAction{kind: actionReady, messageType: pb.MessageType_MSG_NEXT_STAGE_REQUEST, message: &pb.NextStageRequest{}})
	return nil
}

func (p *lifecycleProgress) requireAppliedChoice(applied *pb.RewardApplied) error {
	if applied.Reason != pb.ReasonCode_REASON_OK || applied.EquipmentId != p.validRewardChoice {
		return fmt.Errorf("%w: choice %d returned %s/%d", ErrInvalidReward, p.validRewardChoice, applied.Reason, applied.EquipmentId)
	}
	return nil
}

func (p *lifecycleProgress) queueRewardChoice(id uint32) {
	p.actions = append(p.actions, outboundAction{
		kind: actionRewardChoice, messageType: pb.MessageType_MSG_REWARD_CHOICE,
		message: &pb.RewardChoice{EquipmentId: id},
	})
}

func (p *lifecycleProgress) nextAction() (outboundAction, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.actions) == 0 {
		return outboundAction{}, false
	}
	action := p.actions[0]
	p.actions = p.actions[1:]
	return action, true
}

func (p *lifecycleProgress) actionSent(kind actionKind) {
	if kind != actionReady {
		return
	}
	p.mu.Lock()
	p.readySent++
	p.mu.Unlock()
}

func (p *lifecycleProgress) inputIntent() (*pb.Vec2, bool, bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phase != PhaseCombat {
		return nil, false, false, false
	}
	stage, _ := p.combat.position()
	usePotion := p.config.UsePotion && stage > 1 && p.potionSentStage != stage
	if usePotion {
		p.potionSentStage = stage
		p.potionInputs++
	}
	aim, shoot := p.combat.combatIntent()
	return aim, shoot, usePotion, true
}

func (p *lifecycleProgress) complete() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase == PhaseComplete && p.stagesCleared >= p.config.TargetStages && p.combat.hasAcknowledgedCombat()
}

func (p *lifecycleProgress) beginResume() Phase {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := p.phase
	p.phase = PhaseResume
	p.recoveryAttempts++
	return previous
}

func (p *lifecycleProgress) resumed(previous Phase) {
	p.mu.Lock()
	p.phase = previous
	p.recoverySucceeded++
	p.mu.Unlock()
}

func (p *lifecycleProgress) setDisconnect(reason string) {
	p.mu.Lock()
	p.disconnectReason = reason
	p.mu.Unlock()
}

func (p *lifecycleProgress) result(clientID int, sessionID, playerID uint64) MatchResult {
	stage, tick := p.combat.position()
	reloads, samples := p.combat.ammoDiagnostics()
	p.mu.Lock()
	defer p.mu.Unlock()
	return MatchResult{
		ClientID: clientID, Phase: p.phase, SessionID: sessionID, PlayerID: playerID, RoomID: p.roomID,
		StageIndex: stage, LastServerTick: tick, StagesCleared: p.stagesCleared,
		ReloadsStarted: reloads, DirectorSamples: samples,
		RewardsApplied: p.rewardsApplied, ReadySent: p.readySent, PotionInputs: p.potionInputs,
		RecoveryAttempts: p.recoveryAttempts, RecoverySucceeded: p.recoverySucceeded, DisconnectReason: p.disconnectReason,
	}
}

func deterministicRewardChoice(seed uint64, clientID int, stage uint32, options []uint32) uint32 {
	x := seed ^ uint64(clientID+1)*0x9e3779b97f4a7c15 ^ uint64(stage)*0xbf58476d1ce4e5b9
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	return options[x%uint64(len(options))]
}

func invalidEquipmentID(offered map[uint32]struct{}) uint32 {
	for candidate := ^uint32(0); candidate > 0; candidate-- {
		if _, exists := offered[candidate]; !exists {
			return candidate
		}
	}
	panic("all equipment ids offered")
}

type serverDisconnectError struct{ message string }

func (e *serverDisconnectError) Error() string { return e.message }

func transientConnectionError(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// RunMatch completes one authoritative multi-stage match. Context duration is
// a deadline, never evidence that a stage or match succeeded.
func RunMatch(ctx context.Context, address string, clientID int, config MatchConfig) (MatchResult, error) {
	result := MatchResult{ClientID: clientID, Phase: PhaseLogin}
	if clientID < 0 {
		return result, fmt.Errorf("%w: client id must not be negative", ErrInvalidMatchConfig)
	}
	if err := config.validate(); err != nil {
		return result, err
	}

	dial := func() (net.Conn, *bufio.Reader, error) {
		dialer := net.Dialer{Timeout: 3 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, nil, err
		}
		return conn, bufio.NewReader(conn), nil
	}
	conn, reader, err := dial()
	if err != nil {
		return result, err
	}
	defer func() { _ = conn.Close() }()

	var frameSequence uint32
	send := func(messageType pb.MessageType, message proto.Message) error {
		frameSequence++
		return sendMessage(conn, messageType, frameSequence, message)
	}
	if err := send(pb.MessageType_MSG_LOGIN_REQUEST, &pb.LoginRequest{
		ProtocolVersion: 2, Token: "dev", DisplayName: fmt.Sprintf("bot-%d", clientID),
	}); err != nil {
		return result, err
	}
	var login pb.LoginResponse
	if err := readUntil(ctx, conn, reader, pb.MessageType_MSG_LOGIN_RESPONSE, &login); err != nil {
		return result, err
	}
	if login.Reason != pb.ReasonCode_REASON_OK || login.SessionId == 0 || login.PlayerId == 0 {
		return result, fmt.Errorf("login rejected: %s", login.Reason)
	}
	result.SessionID = login.SessionId
	result.PlayerID = login.PlayerId
	result.Phase = PhaseMatch

	if err := send(pb.MessageType_MSG_MATCH_REQUEST, &pb.MatchRequest{}); err != nil {
		return result, err
	}
	var found pb.MatchFound
	if err := readUntil(ctx, conn, reader, pb.MessageType_MSG_MATCH_FOUND, &found); err != nil {
		return result, err
	}
	if found.RoomId == 0 {
		return result, errors.New("match returned zero room id")
	}
	_ = conn.SetReadDeadline(time.Time{})
	progress := newLifecycleProgress(config, clientID, found.RoomId)

	inputTicker := time.NewTicker(time.Second / 30)
	defer inputTicker.Stop()
	readErrors := make(chan error, 1)
	stateChanges := make(chan struct{}, 1)
	startReader := func(activeReader *bufio.Reader) {
		go func() {
			for {
				messageType, body, err := readFrame(activeReader)
				if err != nil {
					readErrors <- err
					return
				}
				if err := progress.observe(messageType, body); err != nil {
					readErrors <- err
					return
				}
				select {
				case stateChanges <- struct{}{}:
				default:
				}
			}
		}()
	}
	startReader(reader)

	var inputSequence uint32
	resumeToken := slices.Clone(login.ResumeToken)
	for {
		select {
		case <-ctx.Done():
			result = progress.result(clientID, login.SessionId, login.PlayerId)
			if progress.complete() {
				return result, nil
			}
			if !progress.combat.hasAcknowledgedCombat() {
				return result, fmt.Errorf("%w before shutdown in %s: %v", ErrCombatNotAcknowledged, result.Phase, ctx.Err())
			}
			return result, fmt.Errorf("%w before shutdown in %s: %v", ErrStageNotCleared, result.Phase, ctx.Err())
		case readErr := <-readErrors:
			if progress.complete() {
				return progress.result(clientID, login.SessionId, login.PlayerId), nil
			}
			var disconnect *serverDisconnectError
			canResume := config.EnableResume && len(resumeToken) > 0 &&
				int(progress.result(clientID, login.SessionId, login.PlayerId).RecoveryAttempts) < config.MaxResumeAttempts &&
				!errors.As(readErr, &disconnect) && transientConnectionError(readErr)
			if !canResume {
				progress.setDisconnect(readErr.Error())
				return progress.result(clientID, login.SessionId, login.PlayerId), readErr
			}
			previous := progress.beginResume()
			_ = conn.Close()
			conn, reader, err = dial()
			if err != nil {
				progress.setDisconnect(err.Error())
				return progress.result(clientID, login.SessionId, login.PlayerId), err
			}
			frameSequence = 1
			if err := sendMessage(conn, pb.MessageType_MSG_RESUME_REQUEST, frameSequence, &pb.ResumeRequest{
				ResumeToken: resumeToken, ProtocolVersion: 2,
			}); err != nil {
				progress.setDisconnect(err.Error())
				return progress.result(clientID, login.SessionId, login.PlayerId), err
			}
			var resumed pb.ResumeResponse
			if err := readUntil(ctx, conn, reader, pb.MessageType_MSG_RESUME_RESPONSE, &resumed); err != nil {
				progress.setDisconnect(err.Error())
				return progress.result(clientID, login.SessionId, login.PlayerId), err
			}
			if resumed.Reason != pb.ReasonCode_REASON_OK || resumed.SessionId != login.SessionId || resumed.PlayerId != login.PlayerId {
				err := fmt.Errorf("%w: %s", ErrResumeRejected, resumed.Reason)
				progress.setDisconnect(err.Error())
				return progress.result(clientID, login.SessionId, login.PlayerId), err
			}
			_ = conn.SetReadDeadline(time.Time{})
			progress.resumed(previous)
			startReader(reader)
		case <-stateChanges:
			for {
				action, ok := progress.nextAction()
				if !ok {
					break
				}
				if err := send(action.messageType, action.message); err != nil {
					progress.setDisconnect(err.Error())
					return progress.result(clientID, login.SessionId, login.PlayerId), err
				}
				progress.actionSent(action.kind)
			}
			if progress.complete() {
				return progress.result(clientID, login.SessionId, login.PlayerId), nil
			}
		case now := <-inputTicker.C:
			aim, shoot, usePotion, sendInput := progress.inputIntent()
			if !sendInput {
				continue
			}
			inputSequence++
			direction := float32(1)
			if clientID%2 != 0 {
				direction = -1
			}
			if err := send(pb.MessageType_MSG_PLAYER_INPUT, &pb.PlayerInput{
				InputSeq: inputSequence, ClientTickMs: uint64(now.UnixMilli()), Move: &pb.Vec2{X: direction},
				Aim: aim, Shoot: shoot, UsePotion: usePotion, Reload: progress.combat.reloadIntent(),
			}); err != nil {
				progress.setDisconnect(err.Error())
				return progress.result(clientID, login.SessionId, login.PlayerId), err
			}
			progress.combat.recordInput(inputSequence, shoot)
			if progress.complete() {
				return progress.result(clientID, login.SessionId, login.PlayerId), nil
			}
		}
	}
}
