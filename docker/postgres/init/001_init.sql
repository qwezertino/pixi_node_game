-- Initial schema + seed data for the game database.
--
-- This file runs automatically, exactly once, the first time the Postgres
-- container initializes an empty data directory (standard Postgres image
-- behavior for anything mounted under /docker-entrypoint-initdb.d — see
-- docker-compose.yml). It replaces the old gameConfig.json/units.json files:
-- from here on, these tables ARE the config, in real typed columns.
--
-- To change a value after this has run once: UPDATE the row directly (or use
-- configctl for the game_config keys, which also notifies running servers
-- live). To re-seed from scratch in dev, wipe the postgres data volume
-- (docker/data/postgres) and let this file run again.

-- ─── game_config: live-tunable network/fanout settings ─────────────────────
-- Read+watched at runtime via internal/liveconfig (Postgres source of truth,
-- Redis pub/sub for instant reload — see that package for details).
CREATE TABLE IF NOT EXISTS game_config (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO game_config (key, value) VALUES
    ('max_connections', '12000'),
    ('rate_limit_msg_sec', '12000'),
    ('rate_limit_burst', '20000'),
    ('ip_conn_rate', '0'),
    ('ip_conn_burst', '20'),
    ('fanout_max_broadcast_bytes_per_tick', '0'),
    ('velocity_replication', 'true'),
    ('keyframe_divisor', '100'),
    ('fanout_queue_shed_depth', '6'),
    ('fanout_drop_streak', '120'),
    ('write_batch_size', '8'),
    ('fanout_fair_debt_max', '12'),
    ('fanout_fair_debt_inc', '1'),
    ('fanout_fair_debt_dec', '2'),
    ('fanout_fair_debt_weight_ns', '250000'),
    ('fanout_round_robin_weight_ns', '150000'),
    ('fanout_critical_window_ms', '400'),
    ('fanout_critical_boost_ns', '3000000'),
    ('fanout_min_recipients_per_tick', '256'),
    ('fanout_max_recipients_per_tick', '0'),
    ('fanout_target_ms', '6'),
    ('world_state_active_staleness_ms', '220'),
    ('world_state_idle_staleness_ms', '650'),
    ('world_state_active_window_ms', '1000'),
    ('spawn_min_x', '1500'),
    ('spawn_max_x', '3000'),
    ('spawn_min_y', '500'),
    ('spawn_max_y', '1500')
ON CONFLICT (key) DO NOTHING;

-- ─── game_settings: static game/world rules, shared with the TS client ─────
-- Singleton row (id always 1). Loaded once at server startup — see
-- internal/config — and served to the client at GET /api/config. No
-- live-reload: tick rate and world size are baked into precomputed tables
-- and an already-bound listener, so changing this requires a restart.
CREATE TABLE IF NOT EXISTS game_settings (
    id                      SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    tick_rate               INTEGER NOT NULL,
    sync_interval_sec       INTEGER NOT NULL,
    units_per_meter         DOUBLE PRECISION NOT NULL,
    world_width             INTEGER NOT NULL,
    world_height            INTEGER NOT NULL,
    spawn_min_x             INTEGER NOT NULL,
    spawn_max_x             INTEGER NOT NULL,
    spawn_min_y             INTEGER NOT NULL,
    spawn_max_y             INTEGER NOT NULL,
    player_base_scale       DOUBLE PRECISION NOT NULL,
    debug_mode              BOOLEAN NOT NULL,
    world_background_color  TEXT NOT NULL
);

INSERT INTO game_settings (
    id, tick_rate, sync_interval_sec, units_per_meter,
    world_width, world_height,
    spawn_min_x, spawn_max_x, spawn_min_y, spawn_max_y,
    player_base_scale, debug_mode, world_background_color
) VALUES (
    1, 20, 30, 10,
    6000, 3000,
    1500, 3000, 500, 1500,
    2, false, '#808080'
) ON CONFLICT (id) DO NOTHING;

-- ─── units: per-unit-type stats, shared with the TS client ─────────────────
-- Loaded once at server startup and served to the client at GET /api/units.
-- No live-reload — see internal/units.LoadDefinitions and the note in
-- internal/game/world.go about precomputed per-unit move/stamina/attack
-- tables.
CREATE TABLE IF NOT EXISTS units (
    type_id                                 SMALLINT PRIMARY KEY,
    id                                       TEXT NOT NULL UNIQUE,
    display_name                            TEXT NOT NULL,
    tier                                     TEXT NOT NULL,

    hp                                       DOUBLE PRECISION NOT NULL,
    passive_dr                               DOUBLE PRECISION NOT NULL DEFAULT 0,
    move_speed                               DOUBLE PRECISION NOT NULL,
    range_type                               TEXT NOT NULL,
    range                                    DOUBLE PRECISION NOT NULL,
    damage                                   DOUBLE PRECISION NOT NULL,
    windup_seconds                           DOUBLE PRECISION NOT NULL,
    active_seconds                           DOUBLE PRECISION NOT NULL,
    recovery_seconds                         DOUBLE PRECISION NOT NULL,

    stamina                                  DOUBLE PRECISION NOT NULL,
    stamina_regen_per_second                 DOUBLE PRECISION NOT NULL,
    sprint_speed_multiplier                  DOUBLE PRECISION NOT NULL,
    sprint_stamina_cost_per_second           DOUBLE PRECISION NOT NULL,

    combo_steps                              SMALLINT,
    combo_window_seconds                     DOUBLE PRECISION,

    animation_speed                          DOUBLE PRECISION NOT NULL,
    attack_stamina_cost                      DOUBLE PRECISION,
    draw_hold_threshold_seconds              DOUBLE PRECISION,
    dodge_cost_multiplier                    DOUBLE PRECISION,

    cost_wood                                INTEGER NOT NULL DEFAULT 0,
    cost_stone                               INTEGER NOT NULL DEFAULT 0,
    cost_iron                                INTEGER NOT NULL DEFAULT 0,

    requires_royal_guard                     BOOLEAN NOT NULL DEFAULT FALSE,
    cleave                                   BOOLEAN NOT NULL DEFAULT FALSE,
    has_brace_stance                         BOOLEAN NOT NULL DEFAULT FALSE,

    block_melee_dr                           DOUBLE PRECISION,
    block_ranged_dr                          DOUBLE PRECISION,
    block_drain_per_second                   DOUBLE PRECISION,
    block_recovery_seconds                   DOUBLE PRECISION,

    positional_stamina_cost_reduction_pct    DOUBLE PRECISION,
    positional_min_nearby_allies             SMALLINT,

    opportunist_bow_damage                   DOUBLE PRECISION,
    opportunist_bow_range                    DOUBLE PRECISION,
    opportunist_bow_cooldown_seconds         DOUBLE PRECISION,

    rogue_quiver_damage                      DOUBLE PRECISION,
    rogue_quiver_range                       DOUBLE PRECISION,
    rogue_quiver_charges                     SMALLINT,
    rogue_quiver_recharge_seconds            DOUBLE PRECISION,
    rogue_quiver_execute_multiplier          DOUBLE PRECISION,
    rogue_quiver_execute_hp_threshold_pct    DOUBLE PRECISION,

    recon_view_radius_bonus_pct              DOUBLE PRECISION,
    recon_detection_radius_meters            DOUBLE PRECISION,

    fire_arrow_damage                        DOUBLE PRECISION,
    fire_arrow_structure_damage_multiplier   DOUBLE PRECISION,
    fire_arrow_wood_cost_per_shot            SMALLINT,

    dash_thrust_distance_meters              DOUBLE PRECISION,
    dash_thrust_windup_seconds               DOUBLE PRECISION,
    dash_thrust_recovery_seconds             DOUBLE PRECISION,
    dash_thrust_damage_multiplier            DOUBLE PRECISION,
    dash_thrust_cooldown_seconds             DOUBLE PRECISION,

    anti_shield_multiplier                   DOUBLE PRECISION,
    anti_wood_structure_multiplier           DOUBLE PRECISION,

    asset_path                               TEXT,
    combat_asset_path                        TEXT,
    dash_asset_path                          TEXT
);

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, animation_speed,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path
) VALUES (
    0, 'citizen', 'Citizen', 'citizen', 80, 0, 28.8, 'melee', 0.8, 4,
    0.3, 0.15, 0.22, 80, 12,
    1.5, 10, 0.1,
    0, 0, 0, 'actual/citizens/Masc.%20Citizens/Artun/Artun.png', 'actual/citizens/Masc.%20Citizens/Artun/Artun_Combat.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, combo_steps, combo_window_seconds,
    animation_speed, attack_stamina_cost, has_brace_stance,
    block_melee_dr, block_ranged_dr, block_drain_per_second,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path
) VALUES (
    1, 'spearman', 'Spearman', 'guard', 90, 0, 27.0, 'melee', 1.7, 22,
    0.35, 0.12, 0.20, 100, 10,
    1.5, 10, 2, 0.6,
    0.1, 15, true,
    0.25, 0, 20,
    2, 0, 3, 'actual/guards/Guard_Spearman.png', 'actual/guards/Guard_Spearman_Combat.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, animation_speed,
    draw_hold_threshold_seconds,
    block_melee_dr, block_ranged_dr, block_drain_per_second,
    fire_arrow_damage, fire_arrow_structure_damage_multiplier, fire_arrow_wood_cost_per_shot,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path
) VALUES (
    2, 'archer', 'Archer', 'archer', 65, 0, 28.0, 'ranged', 9.0, 26,
    0.55, 0.0, 0.12, 70, 12,
    1.5, 10, 0.1,
    1.5,
    0.15, 0, 26,
    18, 2, 1,
    3, 0, 1, 'actual/archers/Archer.png', 'actual/archers/Archer_Combat.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, combo_steps, combo_window_seconds,
    animation_speed,
    block_melee_dr, block_ranged_dr, block_drain_per_second,
    positional_stamina_cost_reduction_pct, positional_min_nearby_allies,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path
) VALUES (
    3, 'guard_swordsman', 'Guard Swordsman', 'guard', 110, 0, 26.2, 'melee', 1.1, 16,
    0.25, 0.1, 0.32, 100, 10,
    1.5, 10, 2, 0.6,
    0.1,
    0.7, 0.9, 8,
    25, 2,
    2, 0, 2, 'actual/guards/Guard_Swordsman.png', 'actual/guards/Guard_Swordman_Combat.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, combo_steps, combo_window_seconds,
    animation_speed, attack_stamina_cost, cleave,
    block_melee_dr, block_ranged_dr, block_drain_per_second,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path
) VALUES (
    4, 'greatsword', 'Greatsword', 'guard', 100, 0, 25.2, 'melee', 1.5, 42,
    0.6, 0.2, 0.0, 110, 9,
    1.5, 10, 2, 0.6,
    0.1, 30, true,
    0.3, 0, 18,
    2, 0, 6, 'actual/guards/2-Handed_Swordsman.png', 'actual/guards/2-Handed_Swordsman_Combat.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, combo_steps, combo_window_seconds,
    animation_speed, attack_stamina_cost,
    block_melee_dr, block_ranged_dr, block_drain_per_second,
    opportunist_bow_damage, opportunist_bow_range, opportunist_bow_cooldown_seconds,
    anti_shield_multiplier, anti_wood_structure_multiplier,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path
) VALUES (
    5, 'axe_warrior', 'Axe Warrior', 'warrior', 95, 0, 27.0, 'melee', 1.2, 20,
    0.4, 0.15, 0.12, 100, 10,
    1.5, 10, 2, 0.6,
    0.1, 18,
    0.5, 0.7, 11,
    12, 6, 8,
    2, 2,
    2, 0, 4, 'actual/warriors/Axe_Warrior.png', 'actual/warriors/Axe_Warrior_Combat.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, combo_steps, combo_window_seconds,
    animation_speed, dodge_cost_multiplier,
    block_melee_dr, block_ranged_dr, block_drain_per_second, block_recovery_seconds,
    opportunist_bow_damage, opportunist_bow_range, opportunist_bow_cooldown_seconds,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path
) VALUES (
    6, 'caped_warrior', 'Caped Warrior', 'warrior', 75, 0, 32.4, 'melee', 0.9, 14,
    0.15, 0.08, 0.22, 90, 14,
    1.5, 10, 2, 0.6,
    0.1, 0.5,
    0.35, 0.5, 12, 0.15,
    12, 6, 8,
    3, 0, 2, 'actual/warriors/Caped_Warrior.png', 'actual/warriors/Caped_Warrior_Combat.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, combo_steps, combo_window_seconds,
    animation_speed,
    block_melee_dr, block_ranged_dr, block_drain_per_second, block_recovery_seconds,
    opportunist_bow_damage, opportunist_bow_range, opportunist_bow_cooldown_seconds,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path
) VALUES (
    7, 'skullcap_warrior', 'Skullcap Warrior', 'warrior', 120, 0, 26.2, 'melee', 1.0, 18,
    0.3, 0.1, 0.27, 100, 9,
    1.5, 10, 2, 0.6,
    0.1,
    0.6, 0.8, 7, 0.35,
    12, 6, 8,
    3, 0, 3, 'actual/warriors/Skullcap_Warrior.png', 'actual/warriors/Skullcap_Warrior_Combat.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second,
    animation_speed, dodge_cost_multiplier,
    block_melee_dr, block_ranged_dr, block_drain_per_second,
    rogue_quiver_damage, rogue_quiver_range, rogue_quiver_charges, rogue_quiver_recharge_seconds,
    rogue_quiver_execute_multiplier, rogue_quiver_execute_hp_threshold_pct,
    recon_view_radius_bonus_pct, recon_detection_radius_meters,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path
) VALUES (
    8, 'rogue', 'Rogue', 'warrior', 70, 0, 33.4, 'melee', 0.9, 13,
    0.15, 0.08, 0.25, 95, 15,
    1.5, 10,
    0.1, 0.5,
    0.15, 0, 24,
    12, 6, 3, 4,
    2, 25,
    40, 15,
    3, 0, 3, 'actual/warriors/Rogue_D.png', 'actual/warriors/Rogue_D_Combat.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, combo_steps, combo_window_seconds,
    animation_speed, attack_stamina_cost, cleave,
    block_melee_dr, block_ranged_dr, block_drain_per_second,
    dash_thrust_distance_meters, dash_thrust_windup_seconds, dash_thrust_recovery_seconds,
    dash_thrust_damage_multiplier, dash_thrust_cooldown_seconds,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path, dash_asset_path
) VALUES (
    9, 'heavy_knight', 'Heavy Knight', 'knight', 180, 0.25, 22.6, 'melee', 1.3, 30,
    0.5, 0.15, 0.02, 130, 8,
    1.5, 10, 2, 0.6,
    0.1, 25, true,
    0.55, 0.75, 9,
    3, 0.5, 1.0,
    1.5, 10,
    0, 0, 10, 'actual/knights/Heavy_Knight.png', 'actual/knights/Heavy_Knight_Combat.png', 'actual/knights/Heavy_Knight_Thrust_Dash.png'
) ON CONFLICT (type_id) DO NOTHING;

INSERT INTO units (
    type_id, id, display_name, tier, hp, passive_dr, move_speed, range_type, range, damage,
    windup_seconds, active_seconds, recovery_seconds, stamina, stamina_regen_per_second,
    sprint_speed_multiplier, sprint_stamina_cost_per_second, combo_steps, combo_window_seconds,
    animation_speed, attack_stamina_cost, cleave, requires_royal_guard,
    block_melee_dr, block_ranged_dr, block_drain_per_second,
    dash_thrust_distance_meters, dash_thrust_windup_seconds, dash_thrust_recovery_seconds,
    dash_thrust_damage_multiplier, dash_thrust_cooldown_seconds,
    cost_wood, cost_stone, cost_iron, asset_path, combat_asset_path, dash_asset_path
) VALUES (
    10, 'paladin', 'Paladin', 'knight', 190, 0.27, 23.4, 'melee', 1.3, 32,
    0.5, 0.15, 0.02, 135, 8,
    1.5, 10, 2, 0.6,
    0.1, 25, true, true,
    0.57, 0.77, 9,
    3, 0.5, 1.0,
    1.5, 10,
    0, 2, 10, 'actual/knights/Paladin.png', 'actual/knights/Paladin_Combat.png', 'actual/knights/Paladin_Combat_Thrust_Dash.png'
) ON CONFLICT (type_id) DO NOTHING;
