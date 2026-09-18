package game

import (
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

// The four cover blocks use arena-relative coordinates. Keep these fractions
// aligned with client/src/sync/Arena.h so rendering and prediction agree.
type coverBlock struct{ min, max entity.Vec2 }

func (w *World) coverBlocks() []coverBlock {
	if !w.config.CoverEnabled {
		return nil
	}
	width, height := w.config.Max.X-w.config.Min.X, w.config.Max.Y-w.config.Min.Y
	blocks := make([]coverBlock, 0, 4)
	for _, corner := range [][2]float64{{0.28, 0.56}, {0.535, 0.28}, {0.65, 0.41}, {0.395, 0.65}} {
		block := coverBlock{
			min: entity.Vec2{X: w.config.Min.X + width*corner[0], Y: w.config.Min.Y + height*corner[1]},
			max: entity.Vec2{X: w.config.Min.X + width*(corner[0]+0.07), Y: w.config.Min.Y + height*(corner[1]+0.07)},
		}
		if !pointInBlock(w.config.Spawn, block, 0.5) {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func pointInBlock(point entity.Vec2, block coverBlock, radius float64) bool {
	return point.X > block.min.X-radius && point.X < block.max.X+radius &&
		point.Y > block.min.Y-radius && point.Y < block.max.Y+radius
}

// Move one axis at a time so players and monsters slide along cover edges.
func (w *World) moveWithCover(from, delta entity.Vec2, radius float64) entity.Vec2 {
	result := from
	for axis := range 2 {
		candidate := result
		if axis == 0 {
			candidate.X = math.Max(w.config.Min.X, math.Min(w.config.Max.X, candidate.X+delta.X))
		} else {
			candidate.Y = math.Max(w.config.Min.Y, math.Min(w.config.Max.Y, candidate.Y+delta.Y))
		}
		blocked := false
		for _, cover := range w.coverBlocks() {
			if pointInBlock(candidate, cover, radius) {
				blocked = true
				break
			}
		}
		if !blocked {
			result = candidate
		}
	}
	return result
}

func (w *World) clearSpawnFromCover(position entity.Vec2, radius float64) entity.Vec2 {
	for _, cover := range w.coverBlocks() {
		if !pointInBlock(position, cover, radius) {
			continue
		}
		origin := position
		candidates := []entity.Vec2{
			{X: cover.min.X - radius - 0.02, Y: origin.Y},
			{X: cover.max.X + radius + 0.02, Y: origin.Y},
			{X: origin.X, Y: cover.min.Y - radius - 0.02},
			{X: origin.X, Y: cover.max.Y + radius + 0.02},
		}
		bestDistance := math.Inf(1)
		for _, candidate := range candidates {
			if candidate.X < w.config.Min.X || candidate.X > w.config.Max.X ||
				candidate.Y < w.config.Min.Y || candidate.Y > w.config.Max.Y {
				continue
			}
			blocked := false
			for _, other := range w.coverBlocks() {
				blocked = blocked || pointInBlock(candidate, other, radius)
			}
			if distance := math.Hypot(origin.X-candidate.X, origin.Y-candidate.Y); !blocked && distance < bestDistance {
				position, bestDistance = candidate, distance
			}
		}
	}
	return position
}

// Returns the first point of contact along the segment against expanded cover.
func segmentBlock(from, to entity.Vec2, block coverBlock, radius float64) (float64, bool) {
	enter, leave := 0.0, 1.0
	for _, axis := range [][4]float64{{from.X, to.X - from.X, block.min.X - radius, block.max.X + radius},
		{from.Y, to.Y - from.Y, block.min.Y - radius, block.max.Y + radius}} {
		if axis[1] == 0 {
			if axis[0] < axis[2] || axis[0] > axis[3] {
				return 0, false
			}
			continue
		}
		a, b := (axis[2]-axis[0])/axis[1], (axis[3]-axis[0])/axis[1]
		if a > b {
			a, b = b, a
		}
		enter, leave = math.Max(enter, a), math.Min(leave, b)
		if enter > leave {
			return 0, false
		}
	}
	return enter, true
}

func (w *World) firstCoverHit(from, to entity.Vec2, radius float64) (float64, bool) {
	first := math.Inf(1)
	for _, cover := range w.coverBlocks() {
		if hit, ok := segmentBlock(from, to, cover, radius); ok && hit < first {
			first = hit
		}
	}
	return first, !math.IsInf(first, 1)
}

func (w *World) routeAroundCover(from, goal entity.Vec2, radius float64) entity.Vec2 {
	if _, blocked := w.firstCoverHit(from, goal, radius); !blocked {
		return goal
	}
	best, bestDistance := goal, math.Inf(1)
	for _, block := range w.coverBlocks() {
		if _, blocksRoute := segmentBlock(from, goal, block, radius); !blocksRoute {
			continue
		}
		padding := radius + 0.2
		for _, corner := range []entity.Vec2{
			{X: block.min.X - padding, Y: block.min.Y - padding},
			{X: block.min.X - padding, Y: block.max.Y + padding},
			{X: block.max.X + padding, Y: block.min.Y - padding},
			{X: block.max.X + padding, Y: block.max.Y + padding},
		} {
			if _, blocked := w.firstCoverHit(from, corner, radius); blocked {
				continue
			}
			toCorner := math.Hypot(from.X-corner.X, from.Y-corner.Y)
			if toCorner < 0.1 {
				continue
			}
			distance := toCorner + math.Hypot(goal.X-corner.X, goal.Y-corner.Y)
			if _, stillBlocked := w.firstCoverHit(corner, goal, radius); stillBlocked {
				distance += 1000
			}
			if distance < bestDistance {
				best, bestDistance = corner, distance
			}
		}
	}
	return best
}
