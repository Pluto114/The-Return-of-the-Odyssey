// Package config 从环境变量和可选 .env 文件加载游戏服务端配置。
//
// 支持 configs/.env.example 中记录的 ODYSSEY_* 配置项。
package config

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config 保存服务端运行配置，字段与 configs/.env.example 中的 ODYSSEY_* 环境变量一一对应。
type Config struct {
	Env                      string // 开发环境或生产环境
	TCPAddr                  string
	AdminAddr                string
	MetricsAddr              string
	PprofAddr                string
	TickHz                   int
	SnapshotHz               int
	AIHz                     int
	LogLevel                 string
	MySQLDSN                 string
	RedisAddr                string
	RedisPass                string
	RedisDB                  int
	ResumeEnabled            bool
	ResumeTTLSeconds         int
	RedisOperationMS         int
	ResultsEnabled           bool
	ResultQueueCapacity      int
	ResultMaxAttempts        int
	ResultAttemptTimeoutMS   int
	ResultRetryBackoffMS     int
	ResultShutdownTimeoutSec int
	ResultDeadLetterPath     string

	EquipmentCatalogPath       string
	RewardDurationSec          int
	StageLimit                 int
	FirstStageSeedBase         int64
	DirectorMinDifficulty      float64
	DirectorMaxDifficulty      float64
	DirectorTargetClearTimeSec float64
	DirectorTargetDPS          float64
	DirectorTargetDamageTaken  float64

	// LoginTimeoutSec 是连接后等待 LoginRequest 的最长秒数，默认 5 秒。
	LoginTimeoutSec int

	parseErr error
}

// Default 返回内置默认值，与 configs/.env.example 一致；没有 .env 时服务端也能运行。
func Default() *Config {
	return &Config{
		Env:                        "development",
		TCPAddr:                    "127.0.0.1:7777",
		AdminAddr:                  "127.0.0.1:8080",
		MetricsAddr:                "0.0.0.0:19091", // 集成主机已占用 9091
		PprofAddr:                  "127.0.0.1:6060",
		TickHz:                     30,
		SnapshotHz:                 10,
		AIHz:                       10,
		LogLevel:                   "debug",
		MySQLDSN:                   "odyssey:odyssey_local_only@tcp(127.0.0.1:13306)/odyssey?parseTime=true&charset=utf8mb4&loc=UTC",
		RedisAddr:                  "127.0.0.1:6379",
		RedisPass:                  "odyssey_redis_local_only",
		RedisDB:                    0,
		ResumeEnabled:              false,
		ResumeTTLSeconds:           30,
		RedisOperationMS:           2000,
		ResultsEnabled:             false,
		ResultQueueCapacity:        256,
		ResultMaxAttempts:          3,
		ResultAttemptTimeoutMS:     2000,
		ResultRetryBackoffMS:       100,
		ResultShutdownTimeoutSec:   5,
		ResultDeadLetterPath:       "var/odyssey/result-dead-letter.jsonl",
		EquipmentCatalogPath:       "data/equipment/catalog.json",
		RewardDurationSec:          10,
		StageLimit:                 12,
		FirstStageSeedBase:         1,
		DirectorMinDifficulty:      0.5,
		DirectorMaxDifficulty:      10,
		DirectorTargetClearTimeSec: 45,
		DirectorTargetDPS:          30,
		DirectorTargetDamageTaken:  50,
		LoginTimeoutSec:            5,
	}
}

// Load 先读取可选 envPath 覆盖默认值，再用进程中已有的 ODYSSEY_* 环境变量覆盖文件值；
// 因此真实环境变量优先级最高。envPath 为空时跳过文件。
func Load(envPath string) (*Config, error) {
	cfg := Default()

	if envPath != "" {
		if err := loadDotEnv(envPath, func(k, v string) {
			// 进程环境中已存在的键不从文件覆盖，真实环境变量优先。
			if _, exists := os.LookupEnv(k); exists {
				return
			}
			applyKey(cfg, k, v)
		}); err != nil {
			return nil, err
		}
	}

	// 最后叠加进程环境变量。
	for _, e := range os.Environ() {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.HasPrefix(kv[0], "ODYSSEY_") {
			applyKey(cfg, kv[0], kv[1])
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate 校验服务端运行依赖的全部不变量。
func (c *Config) Validate() error {
	if c.parseErr != nil {
		return c.parseErr
	}
	if c.TickHz <= 0 {
		return fmt.Errorf("config: ODYSSEY_TICK_HZ must be positive, got %d", c.TickHz)
	}
	if c.SnapshotHz <= 0 || c.SnapshotHz > c.TickHz {
		return fmt.Errorf("config: ODYSSEY_SNAPSHOT_HZ must be in (0, %d], got %d", c.TickHz, c.SnapshotHz)
	}
	if c.TCPAddr == "" {
		return fmt.Errorf("config: ODYSSEY_TCP_ADDR must not be empty")
	}
	if err := validateLoopbackListenAddr(c.AdminAddr); err != nil {
		return fmt.Errorf("config: ODYSSEY_ADMIN_ADDR: %w", err)
	}
	if c.Env == "production" && !c.ResumeEnabled {
		return fmt.Errorf("config: ODYSSEY_RESUME_ENABLED must be true in production")
	}
	if c.Env == "production" && !c.ResultsEnabled {
		return fmt.Errorf("config: ODYSSEY_RESULTS_ENABLED must be true in production")
	}
	if c.ResultsEnabled && (strings.TrimSpace(c.MySQLDSN) == "" || c.ResultQueueCapacity < 1 || c.ResultQueueCapacity > 100000 ||
		c.ResultMaxAttempts < 1 || c.ResultMaxAttempts > 100 || c.ResultAttemptTimeoutMS < 10 || c.ResultAttemptTimeoutMS > 30000 ||
		c.ResultRetryBackoffMS < 0 || c.ResultRetryBackoffMS > 30000 || c.ResultShutdownTimeoutSec < 1 ||
		strings.TrimSpace(c.ResultDeadLetterPath) == "") {
		return fmt.Errorf("config: enabled result persistence has invalid MySQL, queue, retry, timeout, shutdown, or dead-letter settings")
	}
	if c.ResumeEnabled {
		if strings.TrimSpace(c.RedisAddr) == "" || c.RedisDB < 0 || c.ResumeTTLSeconds < 1 || c.ResumeTTLSeconds > 3600 || c.RedisOperationMS < 10 || c.RedisOperationMS > 30000 {
			return fmt.Errorf("config: enabled Resume requires Redis address, non-negative DB, TTL in [1, 3600]s and operation timeout in [10, 30000]ms")
		}
		host, port, err := net.SplitHostPort(c.RedisAddr)
		if err != nil {
			return fmt.Errorf("config: ODYSSEY_REDIS_ADDR: %w", err)
		}
		portNumber, err := strconv.Atoi(port)
		if strings.TrimSpace(host) == "" || err != nil || portNumber < 1 || portNumber > 65535 {
			return fmt.Errorf("config: ODYSSEY_REDIS_ADDR must contain a host and numeric port")
		}
	}
	if strings.TrimSpace(c.EquipmentCatalogPath) == "" {
		return fmt.Errorf("config: ODYSSEY_EQUIPMENT_CATALOG must not be empty")
	}
	if c.RewardDurationSec < 1 || c.RewardDurationSec > 3600 {
		return fmt.Errorf("config: ODYSSEY_REWARD_DURATION_SEC must be in [1, 3600], got %d", c.RewardDurationSec)
	}
	if c.StageLimit < 3 || c.StageLimit > 1000 {
		return fmt.Errorf("config: ODYSSEY_STAGE_LIMIT must be in [3, 1000], got %d", c.StageLimit)
	}
	for key, value := range map[string]float64{
		"ODYSSEY_DIRECTOR_MIN_DIFFICULTY":        c.DirectorMinDifficulty,
		"ODYSSEY_DIRECTOR_MAX_DIFFICULTY":        c.DirectorMaxDifficulty,
		"ODYSSEY_DIRECTOR_TARGET_CLEAR_TIME_SEC": c.DirectorTargetClearTimeSec,
		"ODYSSEY_DIRECTOR_TARGET_DPS":            c.DirectorTargetDPS,
		"ODYSSEY_DIRECTOR_TARGET_DAMAGE_TAKEN":   c.DirectorTargetDamageTaken,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			return fmt.Errorf("config: %s must be a finite positive number, got %v", key, value)
		}
	}
	if c.DirectorMinDifficulty > c.DirectorMaxDifficulty {
		return fmt.Errorf("config: director minimum difficulty must not exceed maximum difficulty")
	}
	return nil
}

func validateLoopbackListenAddr(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("must use a loopback host until Admin authentication is implemented")
	}
	return nil
}

// applyKey 把单个 ODYSSEY_* 键值写入 cfg；未知键直接忽略，以兼容未来新增配置。
func applyKey(cfg *Config, key, val string) {
	switch key {
	case "ODYSSEY_ENV":
		cfg.Env = val
	case "ODYSSEY_TCP_ADDR":
		cfg.TCPAddr = val
	case "ODYSSEY_ADMIN_ADDR":
		cfg.AdminAddr = val
	case "ODYSSEY_METRICS_ADDR":
		cfg.MetricsAddr = val
	case "ODYSSEY_PPROF_ADDR":
		cfg.PprofAddr = val
	case "ODYSSEY_TICK_HZ":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TickHz = n
		}
	case "ODYSSEY_SNAPSHOT_HZ":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.SnapshotHz = n
		}
	case "ODYSSEY_AI_HZ":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.AIHz = n
		}
	case "ODYSSEY_LOG_LEVEL":
		cfg.LogLevel = val
	case "ODYSSEY_MYSQL_DSN":
		cfg.MySQLDSN = val
	case "ODYSSEY_REDIS_ADDR":
		cfg.RedisAddr = val
	case "ODYSSEY_REDIS_PASSWORD":
		cfg.RedisPass = val
	case "ODYSSEY_REDIS_DB":
		applyInt(cfg, key, val, &cfg.RedisDB)
	case "ODYSSEY_RESUME_ENABLED":
		applyBool(cfg, key, val, &cfg.ResumeEnabled)
	case "ODYSSEY_RESUME_TTL_SEC":
		applyInt(cfg, key, val, &cfg.ResumeTTLSeconds)
	case "ODYSSEY_REDIS_OPERATION_TIMEOUT_MS":
		applyInt(cfg, key, val, &cfg.RedisOperationMS)
	case "ODYSSEY_RESULTS_ENABLED":
		applyBool(cfg, key, val, &cfg.ResultsEnabled)
	case "ODYSSEY_RESULT_QUEUE_CAPACITY":
		applyInt(cfg, key, val, &cfg.ResultQueueCapacity)
	case "ODYSSEY_RESULT_MAX_ATTEMPTS":
		applyInt(cfg, key, val, &cfg.ResultMaxAttempts)
	case "ODYSSEY_RESULT_ATTEMPT_TIMEOUT_MS":
		applyInt(cfg, key, val, &cfg.ResultAttemptTimeoutMS)
	case "ODYSSEY_RESULT_RETRY_BACKOFF_MS":
		applyInt(cfg, key, val, &cfg.ResultRetryBackoffMS)
	case "ODYSSEY_RESULT_SHUTDOWN_TIMEOUT_SEC":
		applyInt(cfg, key, val, &cfg.ResultShutdownTimeoutSec)
	case "ODYSSEY_RESULT_DEAD_LETTER_PATH":
		cfg.ResultDeadLetterPath = val
	case "ODYSSEY_EQUIPMENT_CATALOG":
		cfg.EquipmentCatalogPath = val
	case "ODYSSEY_REWARD_DURATION_SEC":
		applyInt(cfg, key, val, &cfg.RewardDurationSec)
	case "ODYSSEY_STAGE_LIMIT":
		applyInt(cfg, key, val, &cfg.StageLimit)
	case "ODYSSEY_FIRST_STAGE_SEED_BASE":
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			cfg.recordParseError(key, val, err)
		} else {
			cfg.FirstStageSeedBase = n
		}
	case "ODYSSEY_DIRECTOR_MIN_DIFFICULTY":
		applyFloat(cfg, key, val, &cfg.DirectorMinDifficulty)
	case "ODYSSEY_DIRECTOR_MAX_DIFFICULTY":
		applyFloat(cfg, key, val, &cfg.DirectorMaxDifficulty)
	case "ODYSSEY_DIRECTOR_TARGET_CLEAR_TIME_SEC":
		applyFloat(cfg, key, val, &cfg.DirectorTargetClearTimeSec)
	case "ODYSSEY_DIRECTOR_TARGET_DPS":
		applyFloat(cfg, key, val, &cfg.DirectorTargetDPS)
	case "ODYSSEY_DIRECTOR_TARGET_DAMAGE_TAKEN":
		applyFloat(cfg, key, val, &cfg.DirectorTargetDamageTaken)
	case "ODYSSEY_LOGIN_TIMEOUT_SEC":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.LoginTimeoutSec = n
		}
	}
}

func applyInt(cfg *Config, key, val string, target *int) {
	n, err := strconv.Atoi(val)
	if err != nil {
		cfg.recordParseError(key, val, err)
		return
	}
	*target = n
}

func applyFloat(cfg *Config, key, val string, target *float64) {
	n, err := strconv.ParseFloat(val, 64)
	if err != nil {
		cfg.recordParseError(key, val, err)
		return
	}
	*target = n
}

func applyBool(cfg *Config, key, val string, target *bool) {
	parsed, err := strconv.ParseBool(val)
	if err != nil {
		cfg.recordParseError(key, val, err)
		return
	}
	*target = parsed
}

func (c *Config) recordParseError(key, val string, err error) {
	if c.parseErr == nil {
		c.parseErr = fmt.Errorf("config: parse %s=%q: %w", key, val, err)
	}
}

// loadDotEnv 解析简单的 KEY=VALUE 文件，支持空行与整行 # 注释，不支持行尾注释，因为
// 合法值（例如密码）可能包含 #。值不会按 shell 规则去引号，应直接书写。
func loadDotEnv(path string, apply func(k, v string)) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 没有 .env 也合法，使用默认值与环境变量
		}
		return fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		apply(k, v)
	}
	return sc.Err()
}
