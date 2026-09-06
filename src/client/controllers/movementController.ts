import { Point } from "pixi.js";
import { InputManager } from "../utils/inputManager";
import { NetworkManager } from "../network/networkManager";
import { AnimationController, PlayerState } from "./animationController";
import { CoordinateConverter } from "../utils/coordinateConverter";
import type { Direction } from "../utils/animationLayout";
import { directionFromDelta } from "../utils/facing";
import { milliUnitsPerTick } from "../utils/movement";
import type { UnitDefinition } from "../../shared/units";

const MAX_PENDING_INPUTS = 256;

interface InputTransition {
    sequence: number;
    dx: number;
    dy: number;
    sprint: boolean;
}

export class MovementController {
    private _isMoving = false;
    private _scale: Point;
    private _networkManager: NetworkManager | null = null;
    private _animationController: AnimationController | null = null;
    private _coordinateConverter: CoordinateConverter | null = null;

    private _virtualPosition = { x: 0, y: 0 };

    // Screen-space position is only recomputed once per fixed physics tick (see
    // update()), but the sprite renders every frame at display refresh rate, which
    // is normally higher than TICK_RATE — without interpolation the sprite sits
    // frozen for several frames and then jumps, which reads as jitter/choppiness,
    // worse the higher moveSpeed is (each tick's jump is proportionally bigger).
    // _currentScreenPosition/_previousScreenPosition bracket the last tick's step;
    // render() blends `this.position` (the actual sprite Point) between them using
    // how far the render loop is into the next not-yet-processed tick.
    private _previousScreenPosition = { x: 0, y: 0 };
    private _currentScreenPosition = { x: 0, y: 0 };

    private _currentMovementVector = { dx: 0, dy: 0 };
    private _currentSprint = false;
    private _staminaProvider: (() => number) | null = null;
    // Server-confirmed blocking flag (see main.ts) — used as a fallback once the
    // round trip lands (e.g. block requested by something other than the local
    // RMB handler). Not the primary signal — see _localBlockRequested.
    private _blockingProvider: (() => boolean) | null = null;
    // Local, zero-round-trip block intent (see requestBlock/releaseBlock, wired to
    // RMB in main.ts). Without this, the client keeps resending the held WASD
    // vector every tick until the server's BLOCK_START confirmation round-trips
    // back — during that window the reasserted movement input immediately
    // cancels the server's freshly-entered block state (see world.go
    // updateBlockDrain), so block would only ever stick by lucky timing. Setting
    // this the instant RMB is pressed stops resending movement from the very next
    // tick, before any round trip is needed.
    private _localBlockRequested = false;
    // This player's own moveSpeed converted to milli-units/tick (GDD §60 World
    // Coordinate Resolution) — see setUnit(). 0 until set, so a missing call is
    // visibly "not moving" rather than a wrong fallback speed.
    private _milliUnitsPerTick = 0;
    // This player's own sprint multiplier (GDD §54) — per-unit, see setUnit().
    private _sprintSpeedMultiplier = 1;
    // Fixed-point remainder (1/1000 world units), mirroring the server's
    // Player.MoveRemainderMilli (world.go updatePlayerPosition) — without this the
    // client's average predicted speed permanently diverges from the server's
    // whenever moveSpeed doesn't convert to a whole number of units/tick, and every
    // ACK visibly snaps the sprite back. Reset to 0 only where a fresh, deterministic
    // replay starts (reconcilePosition, setInitialPosition).
    private _moveRemainderMilli = 0;

    private _inputSequence = 0;
    private _pendingInputs: InputTransition[] = [];
    private _lastAckSequence = 0;
    private _hasAck = false;
    private _suspended = false;
    private _direction: Direction = "right";

    get isMoving() {
        return this._isMoving;
    }

    // Locally predicted sprint state (see update()) — used for the local player's
    // own walk-vs-run animation pick, with zero round-trip latency. Remote players
    // instead use the server-replicated PlayerState.sprinting.
    get isSprinting() {
        return this._currentSprint;
    }

    get scale() {
        return this._scale;
    }

    /**
     * Получить текущую виртуальную позицию игрока
     */
    getVirtualPosition(): { x: number, y: number } {
        return { ...this._virtualPosition };
    }

    /**
     * The authoritative (non-interpolated) screen position — use this over
     * `position` (the rendered sprite Point, which lags by up to one tick, see
     * render()) whenever the exact current predicted position matters, e.g. to
     * stamp onto an outgoing message.
     */
    getScreenPosition(): { x: number, y: number } {
        return { ...this._currentScreenPosition };
    }

    /**
     * Применить движение локально (client-side prediction)
     */
    private applyMovement(dx: number, dy: number, sprint: boolean): void {
        this.advancePosition(this._virtualPosition, dx, dy, sprint);

        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
            this._currentScreenPosition.x = screenPos.x;
            this._currentScreenPosition.y = screenPos.y;
        }
    }

    // Local mirror of the server's stamina gate (see world.go updatePlayerPosition):
    // sprint only speeds prediction up while stamina is known to remain. Reads the
    // live client-side StaminaPredictor (see staminaPredictor.ts), not the stale
    // roster-replicated value — any mismatch is corrected by the existing
    // ACK/reconciliation path like any other divergence.
    private canSprint(): boolean {
        const stamina = this._staminaProvider?.();
        return stamina === undefined || stamina > 0;
    }

    // Wires a live stamina reading (see main.ts's StaminaPredictor) so the local
    // sprint gate reacts within the same frame instead of waiting on the network.
    setStaminaProvider(provider: () => number): void {
        this._staminaProvider = provider;
    }

    // Wires the server-confirmed blocking flag (see main.ts) so update() can stop
    // predicting movement the instant RMB is confirmed, the same as attacking.
    setBlockingProvider(provider: () => boolean): void {
        this._blockingProvider = provider;
    }

    // Call on RMB press (see main.ts), before/alongside sending BLOCK_START — see
    // _localBlockRequested for why this can't wait for the server's confirmation.
    requestBlock(): void {
        this._localBlockRequested = true;
    }

    // Call on RMB release/blur (see main.ts), before/alongside sending BLOCK_END.
    releaseBlock(): void {
        this._localBlockRequested = false;
    }

    // Sets the unit whose moveSpeed drives this player's predicted movement (GDD
    // §60 World Coordinate Resolution) — call once at spawn and again if the unit
    // ever changes.
    setUnit(unit: UnitDefinition): void {
        this._milliUnitsPerTick = milliUnitsPerTick(unit);
        this._sprintSpeedMultiplier = unit.sprintSpeedMultiplier;
    }

    private advancePosition(
        position: { x: number; y: number },
        dx: number,
        dy: number,
        sprint: boolean,
    ): void {
        let milliRate = this._milliUnitsPerTick;
        let rateMultiplier = 1;
        if (sprint) rateMultiplier *= this._sprintSpeedMultiplier;
        // Diagonal movement covers sqrt(2)x the distance of an axis-aligned move
        // per tick unless corrected — mirrors the server's world.go
        // updatePlayerPosition exactly (single combined multiplier, one rounding
        // step, same Math.SQRT1_2/1/math.Sqrt2 constant) so client and server never
        // drift apart from a different rounding order.
        if (dx !== 0 && dy !== 0) rateMultiplier *= Math.SQRT1_2;
        if (rateMultiplier !== 1) milliRate = Math.round(milliRate * rateMultiplier);
        const remainder = this._moveRemainderMilli + milliRate;
        const moveDistance = Math.floor(remainder / 1000);
        this._moveRemainderMilli = remainder % 1000;
        position.x += dx * moveDistance;
        position.y += dy * moveDistance;

        if (this._coordinateConverter) {
            const clamped = this._coordinateConverter.clampToVirtualBounds(position.x, position.y);
            position.x = clamped.x;
            position.y = clamped.y;
        }
    }

    /**
     * Обработать acknowledgment от сервера (server authoritative confirmation)
     */
    handleMovementAcknowledgment(
        acknowledgedPosition: {x: number, y: number},
        inputSequence: number,
    ): void {
        if (this._hasAck && !this.isSequenceNewer(inputSequence, this._lastAckSequence)) return;

        const acknowledged = this._pendingInputs.find(input => input.sequence === inputSequence);
        if (!acknowledged) return;

        this._pendingInputs = this._pendingInputs.filter(input =>
            this.isSequenceNewer(input.sequence, inputSequence)
        );
        this._lastAckSequence = inputSequence;
        this._hasAck = true;
        this.reconcilePosition(acknowledgedPosition);
    }

    /**
     * Пересчет позиции на основе server acknowledgment (server reconciliation)
     */
    private reconcilePosition(serverPosition: {x: number, y: number}): void {
        const reconciledTarget = {
            x: serverPosition.x,
            y: serverPosition.y,
        };

        // The ACK position already includes the acknowledged server step. Replay
        // exactly one predicted step for every input the server has not processed.
        // The remainder restarts from 0 here: the server's actual remainder at the
        // ACKed position is unknown to the client, but replaying deterministically
        // from 0 keeps this reproducible and bounds the divergence to under 1 unit,
        // instead of the old flat-rounded rate's unbounded drift.
        this._moveRemainderMilli = 0;
        for (const input of this._pendingInputs) {
            this.advancePosition(reconciledTarget, input.dx, input.dy, input.sprint);
        }

        // Logical prediction is corrected exactly. In the normal path this is a
        // no-op because replaying unacknowledged inputs reproduces the current
        // client position. Any non-zero correction indicates a real divergence.
        this._virtualPosition.x = reconciledTarget.x;
        this._virtualPosition.y = reconciledTarget.y;

        if (this._coordinateConverter) {
            const clampedPos = this._coordinateConverter.clampToVirtualBounds(
                this._virtualPosition.x,
                this._virtualPosition.y
            );
            this._virtualPosition.x = clampedPos.x;
            this._virtualPosition.y = clampedPos.y;
        }

        // Update screen position after reconciliation. This only moves
        // _currentScreenPosition (not the rendered this.position directly) — any real
        // correction is picked up and smoothed by the next render() interpolation
        // step instead of snapping the sprite instantly.
        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
            this._currentScreenPosition.x = screenPos.x;
            this._currentScreenPosition.y = screenPos.y;
        }
    }

    /**
     * Установить виртуальную позицию (для gameState - редкая синхронизация)
     */
    setVirtualPosition(x: number, y: number): void {
        const deltaX = x - this._virtualPosition.x;
        const deltaY = y - this._virtualPosition.y;

        if (deltaX * deltaX + deltaY * deltaY > 25) {
            this._virtualPosition.x = x;
            this._virtualPosition.y = y;
            this._pendingInputs = [];

            // A jump this large is a real desync, not normal prediction noise —
            // snap instantly instead of letting render() interpolate into it.
            if (this._coordinateConverter) {
                const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
                this._currentScreenPosition.x = screenPos.x;
                this._currentScreenPosition.y = screenPos.y;
                this._previousScreenPosition.x = screenPos.x;
                this._previousScreenPosition.y = screenPos.y;
                this.position.x = screenPos.x;
                this.position.y = screenPos.y;
            }
        }

        if (this._currentMovementVector) {
            this._isMoving = this._currentMovementVector.dx !== 0 || this._currentMovementVector.dy !== 0;
        }
    }

    constructor(
        private input: InputManager,
        private position: Point,
        scale: Point,
    ) {
        this._scale = scale;
        this.input.setLifecycleCallbacks(
            () => this.suspendInput(),
            () => this.resumeInput(),
        );
    }

    setNetworkManager(networkManager: NetworkManager): void {
        this._networkManager = networkManager;
    }

    setAnimationController(animationController: AnimationController): void {
        this._animationController = animationController;
    }

    setCoordinateConverter(converter: CoordinateConverter): void {
        this._coordinateConverter = converter;
    }

    /**
     * Основная функция обновления движения с client-side prediction
     */
    update(_deltaTime: number) {
        if (this._suspended) return false;

        // Snapshot the pre-tick screen position as the interpolation start point —
        // see render().
        this._previousScreenPosition.x = this._currentScreenPosition.x;
        this._previousScreenPosition.y = this._currentScreenPosition.y;

        const keyboardVector = this.getDesiredMovementVector();
        const attacking = this._animationController?.playerState === PlayerState.ATTACKING;
        const blocking = this._localBlockRequested || (this._blockingProvider?.() ?? false);
        // Block overrides movement the same way attack does (GDD §54: block is a
        // stance, not something you hold while still moving) — see world.go
        // TryBlockStart/updateBlockDrain for the server-authoritative side of this.
        const desiredVector = (attacking || blocking) ? { dx: 0, dy: 0 } : keyboardVector;
        const moving = desiredVector.dx !== 0 || desiredVector.dy !== 0;
        // Sprint (GDD §54) only means anything while actually moving — holding
        // Shift at a standstill requests nothing and costs nothing.
        const sprint = moving && this.input.isKeyDown("Shift") && this.canSprint();

        // A held vector must be reaffirmed every tick (dead-man's-switch: the server
        // keeps integrating the last known velocity, so a broken/frozen client has to
        // stop sending to be detected). An unchanged zero vector carries nothing new —
        // the server already holds position, so there is nothing to reaffirm.
        if (moving || this.vectorChanged(desiredVector) || sprint !== this._currentSprint) {
            this.queueInput(desiredVector, sprint);
        }

        this._isMoving = moving;
        if (moving) this.applyMovement(desiredVector.dx, desiredVector.dy, sprint);
        return moving;
    }

    /**
     * Blend the rendered sprite position between the last two ticks' confirmed
     * screen positions. Call once per rendered frame (every app.ticker callback),
     * after the fixed-timestep physics loop, with `alpha` = how far the leftover
     * accumulator is into the next not-yet-processed tick (0..1). Fixes choppy
     * motion from rendering a position that only actually updates at TICK_RATE.
     */
    render(alpha: number): void {
        const a = Math.max(0, Math.min(1, alpha));
        this.position.x = this._previousScreenPosition.x + (this._currentScreenPosition.x - this._previousScreenPosition.x) * a;
        this.position.y = this._previousScreenPosition.y + (this._currentScreenPosition.y - this._previousScreenPosition.y) * a;
    }

    private vectorChanged(vector: { dx: number; dy: number }): boolean {
        return vector.dx !== this._currentMovementVector.dx || vector.dy !== this._currentMovementVector.dy;
    }

    private queueInput(vector: { dx: number; dy: number }, sprint: boolean): void {
        this._currentMovementVector = vector;
        this._currentSprint = sprint;
        this._inputSequence = (this._inputSequence + 1) >>> 0;

        const transition: InputTransition = {
            sequence: this._inputSequence,
            dx: vector.dx,
            dy: vector.dy,
            sprint,
        };
        this._pendingInputs.push(transition);
        if (this._pendingInputs.length > MAX_PENDING_INPUTS) {
            this._pendingInputs.shift();
        }
        this.sendMovementToServer(
            transition.dx,
            transition.dy,
            transition.sequence,
            transition.sprint,
        );
    }

    private suspendInput(): void {
        if (this._suspended) return;
        this._suspended = true;

        if (this._currentMovementVector.dx !== 0 || this._currentMovementVector.dy !== 0) {
            // Send STOP synchronously before requestAnimationFrame is throttled.
            this.queueInput({ dx: 0, dy: 0 }, false);
        }
        this._isMoving = false;
    }

    private resumeInput(): void {
        if (!this._suspended) return;

        this._suspended = false;
    }

    /**
     * Получить желаемое направление движения из нажатых клавиш
     */
    private getDesiredMovementVector(): { dx: number; dy: number } {
        let dx = 0;
        let dy = 0;

        // Physical WASD position (e.code), not the character it types — see
        // InputManager. Correct on any keyboard layout, not just QWERTY.
        if (this.input.isKeyDown("KeyW")) dy = -1;
        if (this.input.isKeyDown("KeyS")) dy = 1;
        if (this.input.isKeyDown("KeyA")) dx = -1;
        if (this.input.isKeyDown("KeyD")) dx = 1;

        return { dx, dy };
    }

    /**
     * Проверить, изменился ли вектор движения
     */
    private isSequenceNewer(next: number, current: number): boolean {
        const delta = (next - current) >>> 0;
        return delta !== 0 && delta < 0x80000000;
    }

    /**
     * Отправить движение на сервер
     */
    private sendMovementToServer(dx: number, dy: number, inputSequence: number, sprint: boolean): void {
        if (!this._networkManager) return;

        this._networkManager.sendMovement(dx, dy, inputSequence, sprint);
    }

    /**
     * Установить начальную позицию
     */
    public setInitialPosition(x: number, y: number): void {
        this._virtualPosition.x = Math.round(x);
        this._virtualPosition.y = Math.round(y);

        // A reconnect spawns a brand-new server-side player, so inputs predicted
        // against the previous session must not be replayed onto the new spawn.
        this._pendingInputs = [];
        this._currentMovementVector = { dx: 0, dy: 0 };
        this._currentSprint = false;
        this._isMoving = false;
        this._inputSequence = 0;
        this._lastAckSequence = 0;
        this._hasAck = false;
        this._moveRemainderMilli = 0;
        this._suspended = document.visibilityState === "hidden";

        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
            this._currentScreenPosition.x = screenPos.x;
            this._currentScreenPosition.y = screenPos.y;
        } else {
            this._currentScreenPosition.x = x;
            this._currentScreenPosition.y = y;
        }
        this._previousScreenPosition.x = this._currentScreenPosition.x;
        this._previousScreenPosition.y = this._currentScreenPosition.y;
        this.position.x = this._currentScreenPosition.x;
        this.position.y = this._currentScreenPosition.y;
    }

    /**
     * Recompute the screen position from the current virtual position — for a
     * coordinate-system change (viewport resize), not a movement. Sets previous,
     * current, and the rendered position together so it's an instant snap rather
     * than something the next render() call would try to interpolate away from.
     */
    public refreshScreenPositionFromVirtual(): void {
        if (!this._coordinateConverter) return;
        const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
        this._currentScreenPosition.x = screenPos.x;
        this._currentScreenPosition.y = screenPos.y;
        this._previousScreenPosition.x = screenPos.x;
        this._previousScreenPosition.y = screenPos.y;
        this.position.x = screenPos.x;
        this.position.y = screenPos.y;
    }

    /**
     * Установить флаг начала атаки (устаревший метод, оставлен для совместимости)
     */
    public setAttackStarted(): void {
        // No longer needed - attack handling is done in update()
    }

    /**
     * Обработчик завершения атаки (устаревший метод, оставлен для совместимости)
     */
    public onAttackEnd(): void {
        // No longer needed - movement resumes automatically after attack
    }

    /**
     * Обновить направление персонажа по позиции курсора относительно игрока —
     * экран вокруг игрока делится на 4 равные четверти (право/лево/вниз/вверх),
     * см. directionFromDelta. Рендер (флип на плейсхолдере, либо выбор кадров
     * нужного направления на реальном спрайте) обрабатывает AnimationController.
     *
     * Заблокировано на время атаки (см. AnimationController.setDirection) — не
     * только рендер игнорирует смену направления, но и на сервер она не уходит,
     * чтобы удар был честно зафиксирован в сторону, куда прицелились до замаха.
     */
    updateFacing(mouseX: number, mouseY: number): void {
        if (this._animationController?.playerState === PlayerState.ATTACKING) return;

        // Uses the authoritative (non-interpolated) screen position: this.position
        // only catches up to _currentScreenPosition on the next render() call, so
        // reading it here — mid fixed-tick-loop, before that render happens — would
        // compute direction against a stale, one-render-frame-behind point.
        const direction = directionFromDelta(mouseX - this._currentScreenPosition.x, mouseY - this._currentScreenPosition.y);
        if (direction === this._direction) return;

        this._direction = direction;
        this._animationController?.setDirection(direction);
        this._networkManager?.sendDirection(direction);
    }
}
