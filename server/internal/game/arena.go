package game

import (
	"math"

	"github.com/Pluto114/The-Return-of-the-Odyssey/server/internal/game/entity"
)

type coverBlock struct{ min, max entity.Vec2 }

func (w *World) coverBlocks() []coverBlock {
	return w.covers
}

type normalizedBlock struct{ minX, minY, maxX, maxY float64 }

// buildCoverBlocks mirrors client/src/sync/Arena.h. Each six-stage cycle uses
// a different authored topology; the stage seed rotates and mirrors it, giving
// later cycles fresh routes without sending redundant geometry on the wire.
func buildCoverBlocks(config Config, stageIndex uint32, stageSeed int64) []coverBlock {
	if !config.CoverEnabled || stageIndex == 0 {
		return nil
	}
	variant := uint64(stageSeed) ^ uint64(stageIndex)*0x9e3779b97f4a7c15
	rotation, mirrored := uint32(variant&3), (variant>>2)&1 != 0
	transform := func(block normalizedBlock) normalizedBlock {
		if mirrored {
			block = normalizedBlock{1 - block.maxX, block.minY, 1 - block.minX, block.maxY}
		}
		for range rotation {
			block = normalizedBlock{1 - block.maxY, block.minX, 1 - block.minY, block.maxX}
		}
		return block
	}

	var authored []normalizedBlock
	switch (stageIndex - 1) % 6 {
	case 0: // Crosswind gates.
		authored = []normalizedBlock{{.23, .15, .28, .42}, {.23, .58, .28, .85},
			{.72, .15, .77, .44}, {.72, .60, .77, .85}, {.39, .27, .61, .32}, {.39, .68, .61, .73}}
	case 1: // Broken ring with four breaches.
		authored = []normalizedBlock{{.27, .27, .44, .31}, {.56, .27, .73, .31},
			{.27, .69, .44, .73}, {.56, .69, .73, .73}, {.27, .34, .31, .46},
			{.27, .54, .31, .66}, {.69, .34, .73, .46}, {.69, .54, .73, .66}}
	case 2: // Twin corridors and crossover baffles.
		authored = []normalizedBlock{{.10, .30, .42, .35}, {.58, .30, .90, .35},
			{.18, .65, .46, .70}, {.54, .65, .82, .70}, {.18, .43, .23, .57}, {.77, .43, .82, .57}}
	case 3: // Spiral relay.
		authored = []normalizedBlock{{.22, .21, .70, .26}, {.70, .21, .75, .59},
			{.39, .59, .75, .64}, {.34, .40, .39, .64}, {.34, .35, .58, .40}, {.58, .35, .63, .50}}
	case 4: // Four L-shaped corner bastions.
		authored = []normalizedBlock{{.14, .20, .35, .25}, {.14, .20, .19, .40},
			{.65, .20, .86, .25}, {.81, .20, .86, .40}, {.14, .75, .35, .80},
			{.14, .60, .19, .80}, {.65, .75, .86, .80}, {.81, .60, .86, .80}}
	case 5: // Staggered L-shaped gauntlet.
		authored = []normalizedBlock{{.10, .19, .34, .24}, {.29, .19, .34, .39},
			{.38, .38, .62, .43}, {.38, .38, .43, .58}, {.66, .61, .90, .66}, {.66, .61, .71, .81}}
	}

	width, height := config.Max.X-config.Min.X, config.Max.Y-config.Min.Y
	blocks := make([]coverBlock, 0, len(authored))
	for _, authoredBlock := range authored {
		block := transform(authoredBlock)
		worldBlock := coverBlock{
			min: entity.Vec2{X: config.Min.X + width*block.minX, Y: config.Min.Y + height*block.minY},
			max: entity.Vec2{X: config.Min.X + width*block.maxX, Y: config.Min.Y + height*block.maxY},
		}
		if !pointInBlock(config.Spawn, worldBlock, .5) {
			blocks = append(blocks, worldBlock)
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
	// Build a compact visibility graph from padded obstacle corners. A single
	// greedy corner works for one crate but gets trapped by L walls and spirals;
	// the graph chooses a complete shortest route through compound cover.
	points := []entity.Vec2{from, goal}
	for _, block := range w.coverBlocks() {
		padding := radius + 0.2
		for _, corner := range []entity.Vec2{
			{X: block.min.X - padding, Y: block.min.Y - padding},
			{X: block.min.X - padding, Y: block.max.Y + padding},
			{X: block.max.X + padding, Y: block.min.Y - padding},
			{X: block.max.X + padding, Y: block.max.Y + padding},
		} {
			if corner.X < w.config.Min.X || corner.X > w.config.Max.X ||
				corner.Y < w.config.Min.Y || corner.Y > w.config.Max.Y {
				continue
			}
			inside := false
			for _, other := range w.coverBlocks() {
				inside = inside || pointInBlock(corner, other, radius+.05)
			}
			if !inside {
				points = append(points, corner)
			}
		}
	}

	const none = -1
	distance := make([]float64, len(points))
	previous := make([]int, len(points))
	visited := make([]bool, len(points))
	for index := range points {
		distance[index], previous[index] = math.Inf(1), none
	}
	distance[0] = 0
	for range points {
		current, best := none, math.Inf(1)
		for index := range points {
			if !visited[index] && distance[index] < best {
				current, best = index, distance[index]
			}
		}
		if current == none || current == 1 {
			break
		}
		visited[current] = true
		for next := range points {
			if next == current || visited[next] {
				continue
			}
			if _, blocked := w.firstCoverHit(points[current], points[next], radius); blocked {
				continue
			}
			candidate := distance[current] + math.Hypot(points[next].X-points[current].X,
				points[next].Y-points[current].Y)
			if candidate < distance[next] {
				distance[next], previous[next] = candidate, current
			}
		}
	}
	if previous[1] != none {
		waypoint := 1
		for previous[waypoint] != 0 && previous[waypoint] != none {
			waypoint = previous[waypoint]
		}
		if previous[waypoint] == 0 {
			return points[waypoint]
		}
	}
	return goal
}
