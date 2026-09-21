package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	resumeTokenPrefix   = "odyssey:resume:v1:"
	maxResumeTokenBytes = 256
	maxSessionKeyBytes  = 256
)

var (
	ErrInvalidRedisClient  = errors.New("redis client must not be nil")
	ErrInvalidResumeTTL    = errors.New("resume token TTL must be positive")
	ErrInvalidResumeToken  = errors.New("resume token must contain 1 to 256 bytes")
	ErrInvalidSessionKey   = errors.New("session key must contain 1 to 256 bytes")
	ErrResumeTokenExists   = errors.New("resume token already exists")
	ErrResumeTokenNotFound = errors.New("resume token not found")
	ErrInvalidResumeRoute  = errors.New("invalid resume route")
	ErrCorruptResumeRoute  = errors.New("corrupt resume route")
	ErrResumeBackend       = errors.New("resume token backend unavailable")
)

const ResumeRouteVersion = 1

// ResumeRoute 是重连后定位在线 Session 与 Room 所需的最小持久绑定，不包含 World 快照。
type ResumeRoute struct {
	Version    uint8  `json:"version"`
	SessionID  uint64 `json:"session_id"`
	PlayerID   uint64 `json:"player_id"`
	RoomID     uint64 `json:"room_id"`
	Generation uint64 `json:"generation"`
}

func (r ResumeRoute) Validate() error {
	if r.Version != ResumeRouteVersion || r.SessionID == 0 || r.PlayerID == 0 || r.RoomID == 0 || r.Generation == 0 {
		return ErrInvalidResumeRoute
	}
	return nil
}

// ResumeTokenStore 在 Redis 中保存一次性恢复令牌；令牌和会话键均为不透明值。
type ResumeTokenStore struct {
	client *redis.Client
	ttl    time.Duration
}

// NewResumeTokenStore 创建 Redis 令牌存储。Issue 成功时由 Redis 设置 TTL，废弃令牌无需
// 服务端进程主动清理也会自动过期。
func NewResumeTokenStore(client *redis.Client, ttl time.Duration) (*ResumeTokenStore, error) {
	if client == nil {
		return nil, ErrInvalidRedisClient
	}
	if ttl <= 0 {
		return nil, ErrInvalidResumeTTL
	}

	return &ResumeTokenStore{client: client, ttl: ttl}, nil
}

// Issue 在 TTL 内把新令牌关联到会话键，已存在令牌不会被覆盖。
func (s *ResumeTokenStore) Issue(ctx context.Context, token, sessionKey string) error {
	if !validOpaqueValue(token, maxResumeTokenBytes) {
		return ErrInvalidResumeToken
	}
	if !validOpaqueValue(sessionKey, maxSessionKeyBytes) {
		return ErrInvalidSessionKey
	}

	stored, err := s.client.SetNX(ctx, s.key(token), sessionKey, s.ttl).Result()
	if err != nil {
		return backendError("issue", err)
	}
	if !stored {
		return ErrResumeTokenExists
	}
	return nil
}

// Consume 原子地返回并删除令牌对应的会话键，并发或重复请求无法用同一令牌恢复两次。
func (s *ResumeTokenStore) Consume(ctx context.Context, token string) (string, error) {
	if !validOpaqueValue(token, maxResumeTokenBytes) {
		return "", ErrInvalidResumeToken
	}

	sessionKey, err := s.client.GetDel(ctx, s.key(token)).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrResumeTokenNotFound
	}
	if err != nil {
		return "", backendError("consume", err)
	}
	return sessionKey, nil
}

// IssueRoute 使用与 Issue 相同的 SETNX+TTL 语义，保存已校验、带版本的绑定关系。
func (s *ResumeTokenStore) IssueRoute(ctx context.Context, token string, route ResumeRoute) error {
	if err := route.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("encode resume route: %w", err)
	}
	return s.Issue(ctx, token, string(payload))
}

// ConsumeRoute 在解码路由前先原子消费令牌；发现损坏值后也无法再次重放。
func (s *ResumeTokenStore) ConsumeRoute(ctx context.Context, token string) (ResumeRoute, error) {
	payload, err := s.Consume(ctx, token)
	if err != nil {
		return ResumeRoute{}, err
	}
	var route ResumeRoute
	if err := json.Unmarshal([]byte(payload), &route); err != nil {
		return ResumeRoute{}, fmt.Errorf("%w: decode: %v", ErrCorruptResumeRoute, err)
	}
	if err := route.Validate(); err != nil {
		return ResumeRoute{}, fmt.Errorf("%w: %v", ErrCorruptResumeRoute, err)
	}
	return route, nil
}

// Revoke 在永久离开或注销时移除令牌，操作幂等；令牌不存在本身就是目标状态。
func (s *ResumeTokenStore) Revoke(ctx context.Context, token string) error {
	if !validOpaqueValue(token, maxResumeTokenBytes) {
		return ErrInvalidResumeToken
	}
	if err := s.client.Del(ctx, s.key(token)).Err(); err != nil {
		return backendError("revoke", err)
	}
	return nil
}

func (s *ResumeTokenStore) key(token string) string {
	digest := sha256.Sum256([]byte(token))
	return resumeTokenPrefix + hex.EncodeToString(digest[:])
}

func validOpaqueValue(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes
}

func backendError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrResumeBackend, operation, err)
}
