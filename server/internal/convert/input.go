// Package convert 负责 DTO 与领域对象之间的转换，是唯一同时了解生成协议类型与 game
// 领域类型的位置。这里没有业务逻辑，只映射字段且不修改状态。
//
// 转换方向：
//   - 入站：protocol DTO -> game 领域对象（本文件）；
//   - 出站：game 领域对象 -> protocol DTO（snapshot.go、events.go）。
//
// 本包不能依赖 network 或 session；它们位于上层，只通过 convert 路由而无需了解映射细节。
package convert

import (
	"errors"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/generated/protocol"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game"
	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

// ErrNilInput 表示调用方传入 nil PlayerInput。Room 不会产生 nil 意图，但网络边界必须
// 防御畸形帧解码出的空值。
var ErrNilInput = errors.New("convert: nil PlayerInput")

// Input 把线路 protocol.PlayerInput 转成 game.Input，并把 float32 坐标扩展为 float64。
// 本函数不校验业务形状，game.Input.Validate 才是唯一规则来源，Room 在入队时调用它。
//
// nil 子消息按零值处理：nil move 表示停止移动，nil aim 表示无瞄准；若 Shoot 同时为
// 零瞄准，之后会被 game.Input.Validate 拒绝。
func Input(in *protocol.PlayerInput) (game.Input, error) {
	if in == nil {
		return game.Input{}, ErrNilInput
	}
	var out game.Input
	out.Seq = in.InputSeq
	if in.Move != nil {
		out.Direction = vec2(in.Move)
	}
	if in.Aim != nil {
		out.Aim = vec2(in.Aim)
	}
	out.Shoot = in.Shoot
	out.UsePotion = in.UsePotion
	out.Reload = in.Reload
	return out, nil
}

// vec2 把线路 Vec2 的 float32 扩展为领域 entity.Vec2 的 float64。扩展本身精确，但线路值
// 已只有 float32 精度。本层不做四舍五入或截断，由领域层校验有限性和范围。
func vec2(v *protocol.Vec2) entity.Vec2 {
	return entity.Vec2{X: float64(v.X), Y: float64(v.Y)}
}
