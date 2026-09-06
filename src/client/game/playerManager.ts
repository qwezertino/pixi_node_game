import { Container, Point, AnimatedSprite } from "pixi.js";
import { NetworkManager } from "../network/networkManager";
import { PlayerState, TICK_RATE } from "../network/protocol/messages";
import { CharacterVisual, SpriteLoader } from "../utils/spriteLoader";
import {
    AnimationController,
    PlayerState as AnimationPlayerState,
} from "../controllers/animationController";
import { unitsPerTick } from "../utils/movement";
import { CoordinateConverter } from "../utils/coordinateConverter";
import { getUnitDefinitionByTypeId, type UnitDefinition } from "../../shared/units";
import type { Direction } from "../utils/animationLayout";
import { StatusBarWidget } from "../ui/statusBar";
import { StaminaPredictor } from "../utils/staminaPredictor";
//import { LagCompensationSystem } from "../utils/lagCompensation";

// Represents a remote player in the game
// Снимок состояния для entity interpolation
interface PositionSnapshot {
    time: number; // performance.now() момент получения от сервера
    x: number;
    y: number;
}

// Replication is 20 Hz by default. One-frame interpolation hides ordinary jitter
// without keeping remote players hundreds of milliseconds in the past.
const MIN_INTERPOLATION_DELAY_MS = 50;
const MAX_INTERPOLATION_DELAY_MS = 150;
const SNAPSHOT_EWMA_ALPHA = 0.15;
const MAX_SNAPSHOTS = 32;

class RemotePlayer {
    sprite: AnimatedSprite;
    animationController: AnimationController;
    // Stats/properties for this player's unit (see shared/units.ts).
    unitDefinition: UnitDefinition;
    // Current HP (see PlayerAttributes) — undefined until the roster entry for this
    // player has arrived. Nothing drains HP yet, so in practice it sits at
    // unitDefinition.hp from spawn onward.
    currentHp: number | undefined;
    // Stamina is predicted locally every frame instead (see staminaPredictor.ts) and
    // corrected whenever a roster entry arrives — the roster's own cadence is too
    // slow now that sprint/block/attacks actually drain it.
    readonly staminaPredictor: StaminaPredictor;
    readonly statusBar = new StatusBarWidget();
    direction: Direction = "right";
    movementVector: { dx: number; dy: number } = { dx: 0, dy: 0 };
    isMoving: boolean = false;
    isBlocking: boolean = false;
    isSprinting: boolean = false;

    // Буфер серверных позиций для интерполяции
    private snapshots: PositionSnapshot[] = [];
    private interpolationDelayMs = 75;
    private interArrivalEwmaMs = 1000 / Math.max(TICK_RATE, 1);
    private jitterEwmaMs = 0;
    private lastSnapshotTimeMs = 0;
    private lastSnapshotSequence = 0;

    // Текущая рендер-позиция (интерполированная)
    public virtualPosition = { x: 0, y: 0 };
    private coordinateConverter: CoordinateConverter | null = null;



    constructor(
        public id: string,
        public position: Point, // Экранная позиция спрайта
        characterVisual: CharacterVisual,
        unitDefinition: UnitDefinition,
        coordinateConverter?: CoordinateConverter,
        virtualPosition?: { x: number; y: number } // Позиция в виртуальном мире
    ) {
        this.unitDefinition = unitDefinition;
        this.staminaPredictor = new StaminaPredictor(unitDefinition);
        this.coordinateConverter = coordinateConverter || null;

        if (virtualPosition) {
            this.virtualPosition.x = Math.round(virtualPosition.x);
            this.virtualPosition.y = Math.round(virtualPosition.y);
        } else {
            if (this.coordinateConverter) {
                const virtualPos = this.coordinateConverter.screenToVirtual(
                    position.x,
                    position.y
                );
                this.virtualPosition.x = virtualPos.x;
                this.virtualPosition.y = virtualPos.y;
            } else {
                this.virtualPosition.x = Math.round(position.x);
                this.virtualPosition.y = Math.round(position.y);
            }
        }
        this.sprite = characterVisual.getAnimation(characterVisual.directional ? "idle_right" : "idle")!;

        if (this.coordinateConverter) {
            const screenPos = this.coordinateConverter.virtualToScreen(this.virtualPosition.x, this.virtualPosition.y);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
            this.sprite.position.copyFrom(this.position);
        } else {
            this.sprite.position.copyFrom(position);
        }

        this.sprite.scale.set(characterVisual.scale);
        this.sprite.animationSpeed = unitDefinition.animationSpeed;
        this.sprite.play();

        this.animationController = new AnimationController(
            characterVisual,
            this.sprite
        );
    }

    /**
     * Advance a player the server deliberately omitted from a frame.
     *
     * Position is a deterministic function of velocity on both sides (`pos += v*speed`
     * per tick, integer), so the server only sends a record when that integration would
     * be wrong — a velocity change, or a world-boundary clamp. For every other player
     * the client reproduces the record the server chose not to send and feeds it to the
     * same snapshot buffer, so the interpolator below is unchanged and cannot tell the
     * difference.
     *
     * Deliberately unclamped: it mirrors the server's prediction, and any clamp the
     * server applied arrives as a real record on the next frame.
     */
    deadReckon(elapsedTicks: number, stateSequence?: number) {
        if (elapsedTicks <= 0) return;

        const last = this.snapshots[this.snapshots.length - 1];
        if (!last) return;

        if (this.movementVector.dx === 0 && this.movementVector.dy === 0) {
            // A stationary player produces the same position every frame. Still push a
            // snapshot: the interpolator needs a steady arrival cadence, and its
            // adaptive delay is driven by that cadence.
            this.pushSnapshot(last.x, last.y, stateSequence);
            return;
        }

        let step = unitsPerTick(this.unitDefinition) * elapsedTicks;
        // Same diagonal-speed correction as movementController.ts/world.go: moving
        // on both axes at once must not cover sqrt(2)x the distance of a
        // straight move.
        if (this.movementVector.dx !== 0 && this.movementVector.dy !== 0) {
            step = Math.round(step * Math.SQRT1_2);
        }
        this.pushSnapshot(
            last.x + this.movementVector.dx * step,
            last.y + this.movementVector.dy * step,
            stateSequence
        );
    }

    // Добавить серверный снимок позиции в буфер
    pushSnapshot(x: number, y: number, stateSequence?: number) {
        if (typeof stateSequence === "number") {
            const seq = stateSequence >>> 0;
            if (this.lastSnapshotSequence !== 0) {
                const delta = (seq - this.lastSnapshotSequence) >>> 0;
                if (delta === 0 || delta >= 0x80000000) {
                    return;
                }
            }
            this.lastSnapshotSequence = seq;
        }

        const now = performance.now();
        if (this.lastSnapshotTimeMs > 0) {
            const dt = now - this.lastSnapshotTimeMs;
            if (dt > 0) {
                this.interArrivalEwmaMs =
                    this.interArrivalEwmaMs * (1 - SNAPSHOT_EWMA_ALPHA) + dt * SNAPSHOT_EWMA_ALPHA;
                const deviation = Math.abs(dt - this.interArrivalEwmaMs);
                this.jitterEwmaMs =
                    this.jitterEwmaMs * (1 - SNAPSHOT_EWMA_ALPHA) + deviation * SNAPSHOT_EWMA_ALPHA;

                const targetDelay = Math.min(
                    MAX_INTERPOLATION_DELAY_MS,
                    Math.max(
                        MIN_INTERPOLATION_DELAY_MS,
                        this.interArrivalEwmaMs * 1.25 + this.jitterEwmaMs * 2
                    )
                );
                this.interpolationDelayMs =
                    this.interpolationDelayMs * (1 - SNAPSHOT_EWMA_ALPHA) + targetDelay * SNAPSHOT_EWMA_ALPHA;
            }
        }
        this.lastSnapshotTimeMs = now;

        this.snapshots.push({ time: now, x, y });
        // Удаляем старые снимки — держим только MAX_SNAPSHOTS
        if (this.snapshots.length > MAX_SNAPSHOTS) {
            this.snapshots.shift();
        }
    }

    update(deltaTime: number) {
        const isAttacking =
            this.animationController.playerState ===
            AnimationPlayerState.ATTACKING;

        this.staminaPredictor.update(deltaTime, { blocking: this.isBlocking, sprinting: this.isSprinting });

        // Entity interpolation: рендерим позицию на adaptive delay в прошлом.
        // Это означает что мы всегда имеем два снимка вокруг целевого времени —
        // никаких экстраполяций и телепортов.
        const renderTime = performance.now() - this.interpolationDelayMs;
        const snaps = this.snapshots;

        if (snaps.length >= 2) {
            const newest = snaps[snaps.length - 1];

            if (renderTime <= newest.time) {
                // Нормальная интерполяция: ищем пару снимков вокруг renderTime
                let newer = newest;
                let older = snaps[snaps.length - 2];

                for (let i = snaps.length - 1; i >= 1; i--) {
                    if (snaps[i - 1].time <= renderTime) {
                        older = snaps[i - 1];
                        newer = snaps[i];
                        break;
                    }
                }

                const span = newer.time - older.time;
                const t = span > 0 ? Math.min(1, (renderTime - older.time) / span) : 1;
                this.virtualPosition.x = older.x + (newer.x - older.x) * t;
                this.virtualPosition.y = older.y + (newer.y - older.y) * t;
            } else {
                // If jitter exhausts the adaptive buffer, hold the newest position.
                // The normal 20 Hz path targets roughly 60–75 ms of history.
                // никакой экстраполяции → никакого телепорта-назад при получении снимка.
                this.virtualPosition.x = newest.x;
                this.virtualPosition.y = newest.y;
            }

            if (this.coordinateConverter) {
                const screenPos = this.coordinateConverter.virtualToScreen(
                    this.virtualPosition.x, this.virtualPosition.y
                );
                this.position.x = screenPos.x;
                this.position.y = screenPos.y;
            }
        } else if (snaps.length === 1) {
            // Только один снимок — просто применяем его
            this.virtualPosition.x = snaps[0].x;
            this.virtualPosition.y = snaps[0].y;
            if (this.coordinateConverter) {
                const screenPos = this.coordinateConverter.virtualToScreen(
                    this.virtualPosition.x, this.virtualPosition.y
                );
                this.position.x = screenPos.x;
                this.position.y = screenPos.y;
            }
        }
        // Если снимков нет — не двигаем, ждём первого gameState

        // Анимация
        this.animationController.setDirection(this.direction);
        if (!isAttacking) {
            if (this.isBlocking) {
                this.animationController.setState(AnimationPlayerState.BLOCKING);
            } else {
                this.animationController.setState(
                    this.isMoving ? AnimationPlayerState.MOVING : AnimationPlayerState.IDLE,
                    this.isSprinting
                );
            }
        }

        this.sprite.position.copyFrom(this.position);

        this.statusBar.update(this.currentHp, this.unitDefinition.hp, this.staminaPredictor.current, this.unitDefinition.stamina);
        this.statusBar.setPosition(this.position.x, this.position.y - this.sprite.height / 2);
    }

    setMovementVector(dx: number, dy: number) {
        this.movementVector.dx = dx;
        this.movementVector.dy = dy;
        this.isMoving = dx !== 0 || dy !== 0;
    }

    setDirection(direction: Direction) {
        this.direction = direction;
    }

    performAttack(comboStep: number) {
        this.movementVector.dx = 0;
        this.movementVector.dy = 0;
        this.isMoving = false;

        this.animationController.handleAttack(comboStep);
    }

    /**
     * Установка начальной позиции (при первом появлении игрока).
     * Добавляет два идентичных снимка чтобы интерполяция сразу работала.
     */
    syncPosition(virtualX: number, virtualY: number) {
        this.virtualPosition.x = virtualX;
        this.virtualPosition.y = virtualY;

        if (this.coordinateConverter) {
            const screenPos = this.coordinateConverter.virtualToScreen(virtualX, virtualY);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
        }

        this.sprite.position.copyFrom(this.position);

        // Заполняем буфер снимков начальной позицией чтобы интерполяция
        // сразу имела данные и игрок был виден
        const now = performance.now();
        this.snapshots = [
            { time: now - this.interpolationDelayMs - 50, x: virtualX, y: virtualY },
            { time: now - this.interpolationDelayMs,      x: virtualX, y: virtualY },
        ];

        this.lastSnapshotTimeMs = now;
    }
}

export class PlayerManager {
    private remotePlayers: Map<string, RemotePlayer> = new Map();
    private pendingPlayers: Map<string, Promise<void>> = new Map();
    private playerContainer: Container;
    private networkManager: NetworkManager;
    private coordinateConverter: CoordinateConverter;
    private movementController: any = null;

    constructor(
        playerContainer: Container,
        networkManager: NetworkManager,
        coordinateConverter: CoordinateConverter
    ) {
        this.playerContainer = playerContainer;
        this.networkManager = networkManager;
        this.coordinateConverter = coordinateConverter;

        this.setupNetworkCallbacks();

        this.processExistingPlayers();
    }

    private setupNetworkCallbacks() {
        this.networkManager.onPlayerJoined((player) => {
            if (player.id === this.networkManager.getPlayerId()) return;
            void this.addRemotePlayer(player);
        });

        this.networkManager.onPlayerLeft((playerId) => {
            this.removeRemotePlayer(playerId);
        });

        this.networkManager.onPlayerMovement((playerId, dx, dy) => {
            const currentPlayerId = this.networkManager.getPlayerId();

            if (playerId === currentPlayerId) {
                return;
            }

            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setMovementVector(dx, dy);
            }
        });

        this.networkManager.onPlayerDirection((playerId, direction) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.setDirection(direction);
            }
        });

        this.networkManager.onPlayerAttack((playerId, _position, comboStep) => {
            const player = this.remotePlayers.get(playerId);
            if (player) {
                player.performAttack(comboStep);
            }
        });

        // A remote player can be created before its unit-roster entry arrives (the
        // roster is a separate, lower-frequency message — see networkManager.ts).
        // Backfill unitDefinition/HP/stamina once it does, instead of leaving them
        // stuck at the default fallback for up to one full-sync cycle.
        this.networkManager.onUnitRoster((entries) => {
            for (const [playerId, attrs] of Object.entries(entries)) {
                const player = this.remotePlayers.get(playerId);
                if (player) {
                    player.unitDefinition = getUnitDefinitionByTypeId(attrs.unitType);
                    player.staminaPredictor.setUnit(player.unitDefinition);
                    player.currentHp = attrs.hp;
                    player.staminaPredictor.reconcile(attrs.stamina);
                }
            }
        });

        this.networkManager.onGameState((players, stateSequence, fullState, elapsedTicks) => {
            const currentPlayerId = this.networkManager.getPlayerId();

            // Players the frame omits did not stand still — the server withheld them
            // because their movement was predictable. Reproduce it before applying the
            // records, so both paths land in the buffer with the same arrival time.
            if (!fullState && elapsedTicks > 0) {
                for (const [playerId, remote] of this.remotePlayers) {
                    if (playerId !== currentPlayerId && !players[playerId]) {
                        remote.deadReckon(elapsedTicks, stateSequence);
                    }
                }
            }

            for (const [playerId, playerState] of Object.entries(players)) {

                if (playerId === currentPlayerId) {
                    // Local player position is managed exclusively by client-side prediction
                    // and ACK-based reconciliation. Do NOT overwrite it from gameState —
                    // server position lags 1+ tick behind and causes jitter.
                    continue;
                }

                const existingPlayer = this.remotePlayers.get(playerId);

                if (existingPlayer) {
                    // Entity interpolation: добавляем снимок в буфер,
                    // позиция будет плавно интерполирована в update()
                    existingPlayer.pushSnapshot(playerState.position.x, playerState.position.y, stateSequence);

                    existingPlayer.direction = playerState.direction;
                    existingPlayer.isMoving = playerState.moving;
                    existingPlayer.isBlocking = playerState.blocking ?? false;
                    existingPlayer.isSprinting = playerState.sprinting ?? false;
                    existingPlayer.setMovementVector(
                        playerState.vx ?? 0,
                        playerState.vy ?? 0
                    );
                } else {
                    void this.addRemotePlayer(playerState);
                }
            }

            // A delta contains only changed players. Absence means removal only in a full snapshot.
            if (fullState) {
                for (const playerId of this.remotePlayers.keys()) {
                    if (playerId !== currentPlayerId && !players[playerId]) {
                        this.removeRemotePlayer(playerId);
                    }
                }
            }
        });
    }

    // Real per-unit spritesheets aren't fully verified for every unit yet (see
    // spriteLoader.ts / animationLayout.ts) — fall back to the placeholder knight
    // sheet for any unit whose sheet doesn't decode cleanly instead of leaving the
    // player invisible.
    private async loadVisualFor(unit: UnitDefinition): Promise<CharacterVisual> {
        try {
            return await SpriteLoader.loadUnitCharacterVisual(unit);
        } catch {
            return SpriteLoader.loadCharacterVisual("/assets/16x16_knight_2_v3.png");
        }
    }

    async addRemotePlayer(playerState: PlayerState) {
        if (this.remotePlayers.has(playerState.id)) {
            return;
        }

        const pending = this.pendingPlayers.get(playerState.id);
        if (pending) {
            return pending;
        }

        const creation = this.createRemotePlayer(playerState)
            .finally(() => this.pendingPlayers.delete(playerState.id));
        this.pendingPlayers.set(playerState.id, creation);
        return creation;
    }

    private async createRemotePlayer(playerState: PlayerState): Promise<void> {
        const unitDefinition = getUnitDefinitionByTypeId(this.networkManager.getUnitType(playerState.id));
        const characterVisual = await this.loadVisualFor(unitDefinition);

        // The player may have left while the shared asset promise was resolving.
        if (this.remotePlayers.has(playerState.id) || !this.networkManager.getPlayers()[playerState.id]) {
            return;
        }

        const screenPos = this.coordinateConverter.virtualToScreen(
            playerState.position.x,
            playerState.position.y
        );
        const position = new Point(screenPos.x, screenPos.y);

        const remotePlayer = new RemotePlayer(
            playerState.id,
            position,
            characterVisual,
            unitDefinition,
            this.coordinateConverter,
            playerState.position
        );

        remotePlayer.direction = playerState.direction;
        remotePlayer.isMoving = playerState.moving;
        remotePlayer.isBlocking = playerState.blocking ?? false;
        remotePlayer.isSprinting = playerState.sprinting ?? false;
        remotePlayer.currentHp = this.networkManager.getHp(playerState.id);
        const knownStamina = this.networkManager.getStamina(playerState.id);
        if (knownStamina !== undefined) remotePlayer.staminaPredictor.reconcile(knownStamina);

        if (playerState.movementVector) {
            remotePlayer.setMovementVector(
                playerState.movementVector.dx,
                playerState.movementVector.dy
            );
        }

        this.playerContainer.addChild(remotePlayer.sprite);
        this.playerContainer.addChild(remotePlayer.statusBar.container);

        this.remotePlayers.set(playerState.id, remotePlayer);
    }

    setMovementController(movementController: any) {
        this.movementController = movementController;
    }

    /**
     * Обновить позиции всех игроков при изменении размеров экрана
     */
    updateAllPlayerPositions(): void {
        if (this.movementController) {
            this.movementController.refreshScreenPositionFromVirtual();
        }

        for (const [, remotePlayer] of this.remotePlayers.entries()) {
            const screenPos = this.coordinateConverter.virtualToScreen(
                remotePlayer.virtualPosition.x,
                remotePlayer.virtualPosition.y
            );
            remotePlayer.position.x = screenPos.x;
            remotePlayer.position.y = screenPos.y;
            remotePlayer.sprite.position.copyFrom(remotePlayer.position);
        }
    }

    removeRemotePlayer(playerId: string) {
        const player = this.remotePlayers.get(playerId);

        if (player) {
            this.playerContainer.removeChild(player.sprite);
            this.playerContainer.removeChild(player.statusBar.container);
            this.remotePlayers.delete(playerId);
            player.sprite.stop();
            player.sprite.destroy();
            player.statusBar.destroy();
        }
    }

    update(deltaTime: number) {
        for (const player of this.remotePlayers.values()) {
            player.update(deltaTime);
        }
    }

    private async processExistingPlayers() {
        const existingPlayers = this.networkManager.getPlayers();
        const currentPlayerId = this.networkManager.getPlayerId();

        for (const [playerId, playerState] of Object.entries(existingPlayers)) {
            if (playerId === currentPlayerId) continue;

            void this.addRemotePlayer(playerState);
        }
    }
}
