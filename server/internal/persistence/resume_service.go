package persistence

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrInvalidResumeServiceOptions = errors.New("invalid resume service options")

type ResumeServiceOptions struct {
	Addr             string
	Password         string
	DB               int
	TokenTTL         time.Duration
	OperationTimeout time.Duration
}

func (o ResumeServiceOptions) Validate() error {
	if strings.TrimSpace(o.Addr) == "" || o.DB < 0 || o.TokenTTL <= 0 || o.OperationTimeout <= 0 {
		return ErrInvalidResumeServiceOptions
	}
	host, port, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return fmt.Errorf("%w: Redis address: %v", ErrInvalidResumeServiceOptions, err)
	}
	portNumber, err := strconv.Atoi(port)
	if strings.TrimSpace(host) == "" || err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("%w: Redis address requires a host and numeric port", ErrInvalidResumeServiceOptions)
	}
	return nil
}

// ResumeService 管理 Redis 客户端生命周期和令牌仓库。启动时执行有超时的 Ping，
// 防止已启用依赖表面启动成功、实际所有恢复操作都必然失败。
type ResumeService struct {
	client *redis.Client
	store  *ResumeTokenStore
}

func OpenResumeService(ctx context.Context, options ResumeServiceOptions) (*ResumeService, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	client := redis.NewClient(&redis.Options{
		Addr: options.Addr, Password: options.Password, DB: options.DB,
		DialTimeout: options.OperationTimeout, ReadTimeout: options.OperationTimeout, WriteTimeout: options.OperationTimeout,
	})
	pingContext, cancel := context.WithTimeout(ctx, options.OperationTimeout)
	defer cancel()
	if err := client.Ping(pingContext).Err(); err != nil {
		_ = client.Close()
		return nil, backendError("startup ping", err)
	}
	store, err := NewResumeTokenStore(client, options.TokenTTL)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return &ResumeService{client: client, store: store}, nil
}

func (s *ResumeService) Store() *ResumeTokenStore {
	if s == nil {
		return nil
	}
	return s.store
}

func (s *ResumeService) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}
