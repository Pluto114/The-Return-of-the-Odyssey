// Package game 实现“服务器权威”的纯游戏模拟：移动、碰撞、战斗、掉落、奖励和关卡状态
// 都以这里的结果为准。它刻意不依赖 socket、protobuf、数据库，也不自行启动 goroutine，
// 因而可以用固定输入做确定性测试。并发由上一层 room 统一管理。
package game

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/equipment"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/reward"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/stage"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/systems"
)

const (
	// 房间以 30Hz 推进权威模拟，每 3 Tick 发布一次快照，即客户端约 10Hz 收到状态。
	// AI 也不必每帧重新决策：10Hz 足以响应玩家，并显著降低寻路与目标选择开销。
	TickRate        = 30
	SnapshotEvery   = 3
	AIDecisionRate  = 10
	AIDecisionEvery = TickRate / AIDecisionRate
	StepSeconds     = 1.0 / TickRate
	TickInterval    = time.Second / TickRate
)

var (
	ErrInvalidPlayer = errors.New("invalid player ID")
	ErrPlayerExists  = errors.New("player already exists")
	ErrPlayerMissing = errors.New("player not in world")
	ErrWorldFull     = errors.New("world is full")
	ErrInvalidInput  = errors.New("invalid movement input")
	ErrStaleInput    = errors.New("stale movement input")
)

type Config struct {
	Capacity     int
	Min, Max     entity.Vec2
	Spawn        entity.Vec2
	MoveSpeed    float64
	InputTimeout time.Duration
	CoverEnabled bool
	StageLimit   uint32
	Combat       CombatConfig
}

func DefaultConfig() Config {
	return Config{Capacity: 2, Max: entity.Vec2{X: 20, Y: 20}, Spawn: entity.Vec2{X: 10, Y: 10},
		MoveSpeed: 5, InputTimeout: 200 * time.Millisecond, CoverEnabled: true, StageLimit: 12, Combat: DefaultCombatConfig()}
}

func (c Config) Validate() error {
	if c.Capacity < 1 || !c.Min.Finite() || !c.Max.Finite() || !c.Spawn.Finite() ||
		c.Min.X >= c.Max.X || c.Min.Y >= c.Max.Y ||
		math.Abs(c.Min.X) > 1e6 || math.Abs(c.Min.Y) > 1e6 || math.Abs(c.Max.X) > 1e6 || math.Abs(c.Max.Y) > 1e6 ||
		c.Spawn.X < c.Min.X || c.Spawn.X > c.Max.X || c.Spawn.Y < c.Min.Y || c.Spawn.Y > c.Max.Y ||
		math.IsNaN(c.MoveSpeed) || math.IsInf(c.MoveSpeed, 0) || c.MoveSpeed <= 0 || c.MoveSpeed > 1e6 || c.InputTimeout <= 0 || !c.Combat.valid() {
		return fmt.Errorf("invalid world configuration")
	}
	return nil
}

// Input 只包含操作意图；玩家身份由 Session 提供，到达时间由服务端记录，二者都不受客户端控制。
type Input struct {
	Seq       uint32
	Direction entity.Vec2
	Aim       entity.Vec2
	Shoot     bool
	UsePotion bool
	Reload    bool
}

func (i Input) Validate() error {
	if i.Seq == 0 || !i.Direction.Finite() || !i.Aim.Finite() || (i.Shoot && i.Aim.X == 0 && i.Aim.Y == 0) {
		return ErrInvalidInput
	}
	return nil
}

type Snapshot struct {
	ServerTick uint64
	Players    []entity.Player
	Monsters   []MonsterView
	Pickups    []PickupView
	Stage      stage.View
}

func (s Snapshot) Clone() Snapshot {
	s.Players = slices.Clone(s.Players)
	s.Monsters = slices.Clone(s.Monsters)
	s.Pickups = slices.Clone(s.Pickups)
	return s
}

type playerState struct {
	player     entity.Player
	input      Input
	receivedAt time.Time
	pending    bool
	nextShot   uint64
	firing     bool
	loadout    equipment.Loadout
}

// World 故意设计成“非并发对象”：只有所属 Room 的 goroutine 可以调用会修改状态的方法。
// 因为写入天然串行，这里无需在 players/monsters/projectiles 每张表上反复加锁，也不会
// 出现同一帧中两个协程以不同顺序结算伤害的问题。Snapshot 会返回脱离 live world 的副本，
// 可安全交给其他 goroutine 编码、广播和统计。
type World struct {
	config  Config
	tick    uint64
	players map[entity.ID]*playerState
	// 永久离开的玩家把最终战斗/装备状态留在终局结果中，但不再出现在实时快照和模拟里。
	departedPlayers  map[entity.ID]PlayerResult
	monsters         map[entity.ID]*monsterState
	projectiles      map[entity.ID]entity.Projectile
	pickups          map[entity.ID]PickupView
	covers           []coverBlock
	nextEntity       entity.ID
	stage            stage.View
	events           []Event
	eventOverflow    bool
	rewardRound      *reward.Round
	rewardCatalog    equipment.Catalog
	rewardUpdates    []RewardUpdate
	rewardOverflow   bool
	performance      stagePerformance
	currentPlan      stage.Plan
	completedStages  []StageSummary
	runStarted       bool
	runStartedAtTick uint64
	runEndedAtTick   uint64
}

func NewWorld(config Config, catalogs ...equipment.Catalog) (*World, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	world := &World{config: config, players: make(map[entity.ID]*playerState), departedPlayers: make(map[entity.ID]PlayerResult), monsters: make(map[entity.ID]*monsterState), projectiles: make(map[entity.ID]entity.Projectile), pickups: make(map[entity.ID]PickupView)}
	if len(catalogs) > 0 {
		world.rewardCatalog = catalogs[0]
	}
	return world, nil
}

func (w *World) AddPlayer(id entity.ID) error {
	if id == 0 || id >= FirstWorldEntityID {
		return ErrInvalidPlayer
	}
	if _, exists := w.players[id]; exists {
		return ErrPlayerExists
	}
	if len(w.players) >= w.config.Capacity {
		return ErrWorldFull
	}
	if w.stage.State != stage.Waiting {
		return ErrStageState
	}
	stats := w.config.Combat.PlayerStats
	stats.MoveSpeed = w.config.MoveSpeed
	delete(w.departedPlayers, id)
	w.players[id] = &playerState{player: entity.Player{ID: id, Position: w.config.Spawn, BaseStats: stats, CurrentStats: stats, Health: stats.MaxHealth, Alive: true, Aim: entity.Vec2{X: 1}}}
	w.players[id].player.Ammo = BaseMagazineCapacity
	w.players[id].player.MagazineCapacity = BaseMagazineCapacity
	w.players[id].player.ReloadDurationTicks = ReloadDurationTicks
	return nil
}

func (w *World) RemovePlayer(id entity.ID) {
	if member, exists := w.players[id]; exists && w.runStarted {
		w.departedPlayers[id] = playerResult(member.player)
	}
	delete(w.players, id)
	if w.rewardRound != nil && w.rewardRound.RemovePlayer(id) && w.stage.State == stage.Reward && w.PlayerCount() > 0 && w.rewardRound.Complete() {
		w.stage.State = stage.PreparingNextStage
	}
}
func (w *World) Clear() {
	clear(w.players)
	clear(w.departedPlayers)
	clear(w.monsters)
	clear(w.projectiles)
	clear(w.pickups)
	w.covers = nil
	w.events = nil
	w.eventOverflow = false
	w.rewardRound = nil
	w.rewardCatalog = equipment.Catalog{}
	w.rewardUpdates = nil
	w.rewardOverflow = false
	w.performance = stagePerformance{}
	w.currentPlan = stage.Plan{}
	w.completedStages = nil
	w.runStarted = false
	w.runStartedAtTick = 0
	w.runEndedAtTick = 0
	w.stage = stage.View{}
}
func (w *World) Close()           { w.Clear(); w.stage.State = stage.Closed }
func (w *World) PlayerCount() int { return len(w.players) }
func (w *World) Tick() uint64     { return w.tick }

// ApplyInput 只暂存该玩家最新的操作意图，不立即移动，也不立即确认。
// Seq 必须递增，receivedAt 使用服务端收包时间，避免客户端伪造时间戳获得额外移动。
// 同一 Tick 到达多包时保留“喝药/换弹”这类一次性动作，防止被后来的移动包覆盖。
func (w *World) ApplyInput(id entity.ID, input Input, receivedAt time.Time) error {
	if err := input.Validate(); err != nil {
		return err
	}
	p, exists := w.players[id]
	if !exists {
		return ErrPlayerMissing
	}
	if input.Seq <= p.input.Seq || receivedAt.Before(p.receivedAt) {
		return ErrStaleInput
	}
	input.Direction = systems.NormalizeDirection(input.Direction)
	// 同一 Tick 到达多包时保留一次性动作。
	input.Reload = input.Reload || (p.pending && p.input.Reload)
	input.UsePotion = input.UsePotion || (p.pending && p.input.UsePotion)
	p.input, p.receivedAt, p.pending = input, receivedAt, true
	return nil
}

// Step 固定只推进 1/30 秒，与本 Tick 收到多少网络包无关。这可以防止“发包越快跑得越快”。
// now 仅用于判断输入是否超时，移动 dt 永远使用 StepSeconds，保证服务端结果稳定。
func (w *World) Step(now time.Time) {
	for _, p := range w.players {
		p.firing = false
		if !p.player.Alive {
			p.player.ReloadTicksRemaining = 0
			p.player.Velocity = entity.Vec2{}
			continue
		}
		direction := entity.Vec2{}
		// 长时间没有新输入时将方向归零，避免玩家掉线后角色继续自动行走。
		fresh := p.input.Seq != 0 && !now.Before(p.receivedAt) && now.Sub(p.receivedAt) < w.config.InputTimeout
		if fresh {
			if w.stage.State == stage.Waiting || w.stage.State == stage.Playing {
				direction = p.input.Direction
			}
			p.firing = w.stage.State == stage.Playing && p.input.Shoot
			if p.input.Aim.X != 0 || p.input.Aim.Y != 0 {
				p.player.Aim = systems.UnitDirection(p.input.Aim)
			}
			if p.pending {
				p.player.LastProcessedInputSeq = p.input.Seq
				if w.stage.State == stage.Playing && p.input.UsePotion {
					w.usePotion(p)
				}
				if w.stage.State == stage.Playing && p.input.Reload {
					startReload(p)
				}
			}
		}
		if !now.Before(p.receivedAt) {
			p.pending = false
		}
		previous := p.player.Position
		systems.Move(&p.player, direction, p.player.CurrentStats.MoveSpeed, StepSeconds, w.config.Min, w.config.Max)
		p.player.Position = w.moveWithCover(previous, entity.Vec2{X: p.player.Position.X - previous.X, Y: p.player.Position.Y - previous.Y}, 0.32)
		p.player.Velocity = entity.Vec2{X: (p.player.Position.X - previous.X) / StepSeconds, Y: (p.player.Position.Y - previous.Y) / StepSeconds}
	}
	w.collectPickups()
	// 顺序固定为玩家移动/拾取 -> tick+1 -> 战斗与 AI -> 奖励状态机。
	// 所有房间都遵守同一顺序，录像回放和测试才可复现。
	w.tick++
	w.stepCombat()
	w.stepReward()
}

// Snapshot 生成完整权威状态，并按实体 ID 确定性排序。返回的 slice 与 live world 分离，
// 网络层可以在另一个 goroutine 中编码它，而不会和下一次 Tick 发生数据竞争。
func (w *World) Snapshot() Snapshot {
	s := Snapshot{ServerTick: w.tick, Players: make([]entity.Player, 0, len(w.players)), Stage: w.stage}
	for _, id := range orderedIDs(w.monsters) {
		m := w.monsters[id].monster
		s.Monsters = append(s.Monsters, MonsterView{ID: m.ID, Position: m.Position, Velocity: m.Velocity, Health: m.Health, MaxHealth: m.CurrentStats.MaxHealth, State: m.State})
	}
	for _, p := range w.players {
		s.Players = append(s.Players, p.player)
	}
	for _, id := range orderedIDs(w.pickups) {
		s.Pickups = append(s.Pickups, w.pickups[id])
	}
	slices.SortFunc(s.Players, func(a, b entity.Player) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return s
}
