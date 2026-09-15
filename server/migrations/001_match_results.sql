CREATE TABLE IF NOT EXISTS match_results (
    match_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    room_id BIGINT UNSIGNED NOT NULL,
    outcome VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    started_tick BIGINT UNSIGNED NOT NULL,
    ended_tick BIGINT UNSIGNED NOT NULL,
    final_stage_index INT UNSIGNED NOT NULL,
    result_json JSON NOT NULL,
    payload_sha256 BINARY(32) NOT NULL,
    created_at DATETIME(6) NOT NULL,
    persisted_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (match_id),
    KEY idx_match_results_created_at (created_at),
    KEY idx_match_results_outcome_created (outcome, created_at),
    CONSTRAINT chk_match_result_outcome CHECK (outcome IN ('victory', 'defeat', 'abandoned')),
    CONSTRAINT chk_match_result_ticks CHECK (ended_tick >= started_tick),
    CONSTRAINT chk_match_result_stage CHECK (final_stage_index > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS match_players (
    match_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    player_id BIGINT UNSIGNED NOT NULL,
    alive BOOLEAN NOT NULL,
    health DOUBLE NOT NULL,
    attack_value DOUBLE NOT NULL,
    defense_value DOUBLE NOT NULL,
    max_health DOUBLE NOT NULL,
    move_speed DOUBLE NOT NULL,
    attack_cooldown_ticks INT UNSIGNED NOT NULL,
    weapon_id INT UNSIGNED NOT NULL,
    relic_id INT UNSIGNED NOT NULL,
    potion_id INT UNSIGNED NOT NULL,
    PRIMARY KEY (match_id, player_id),
    CONSTRAINT fk_match_players_result FOREIGN KEY (match_id) REFERENCES match_results(match_id) ON DELETE CASCADE,
    CONSTRAINT chk_match_player_health CHECK (health >= 0 AND health <= max_health),
    CONSTRAINT chk_match_player_stats CHECK (attack_value >= 0 AND defense_value >= 0 AND max_health > 0 AND move_speed >= 0 AND attack_cooldown_ticks > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
