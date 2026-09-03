import { Point } from "pixi.js";
import { InputManager } from "../utils/inputManager";
import { NetworkManager } from "../network/networkManager";
import { AnimationController, PlayerState } from "./animationController";
import { CoordinateConverter } from "../utils/coordinateConverter";
import { MOVEMENT } from "../../shared/gameConfig";

const MAX_PENDING_INPUTS = 256;

interface InputTransition {
    sequence: number;
    dx: number;
    dy: number;
}

export class MovementController {
    private _isMoving = false;
    private _scale: Point;
    private _networkManager: NetworkManager | null = null;
    private _animationController: AnimationController | null = null;
    private _coordinateConverter: CoordinateConverter | null = null;

    private _virtualPosition = { x: 0, y: 0 };

    private _currentMovementVector = { dx: 0, dy: 0 };

    private _inputSequence = 0;
    private _pendingInputs: InputTransition[] = [];
    private _lastAckSequence = 0;
    private _hasAck = false;
    private _suspended = false;

    get isMoving() {
        return this._isMoving;
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
     * Применить движение локально (client-side prediction)
     */
    private applyMovement(dx: number, dy: number): void {
        this.advancePosition(this._virtualPosition, dx, dy, 1);

        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
        }
    }

    private advancePosition(
        position: { x: number; y: number },
        dx: number,
        dy: number,
        ticks: number,
    ): void {
        const moveDistance = MOVEMENT.playerSpeedPerTick * ticks;
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
        for (const input of this._pendingInputs) {
            this.advancePosition(reconciledTarget, input.dx, input.dy, 1);
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

        // Update screen position after reconciliation
        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
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
        }

        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
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

        const keyboardVector = this.getDesiredMovementVector();
        const attacking = this._animationController?.playerState === PlayerState.ATTACKING;
        const desiredVector = attacking ? { dx: 0, dy: 0 } : keyboardVector;
        const moving = desiredVector.dx !== 0 || desiredVector.dy !== 0;

        // A held vector must be reaffirmed every tick (dead-man's-switch: the server
        // keeps integrating the last known velocity, so a broken/frozen client has to
        // stop sending to be detected). An unchanged zero vector carries nothing new —
        // the server already holds position, so there is nothing to reaffirm.
        if (moving || this.vectorChanged(desiredVector)) {
            this.queueInput(desiredVector);
        }

        this._isMoving = moving;
        if (moving) this.applyMovement(desiredVector.dx, desiredVector.dy);
        return moving;
    }

    private vectorChanged(vector: { dx: number; dy: number }): boolean {
        return vector.dx !== this._currentMovementVector.dx || vector.dy !== this._currentMovementVector.dy;
    }

    private queueInput(vector: { dx: number; dy: number }): void {
        this._currentMovementVector = vector;
        this._inputSequence = (this._inputSequence + 1) >>> 0;

        const transition: InputTransition = {
            sequence: this._inputSequence,
            dx: vector.dx,
            dy: vector.dy,
        };
        this._pendingInputs.push(transition);
        if (this._pendingInputs.length > MAX_PENDING_INPUTS) {
            this._pendingInputs.shift();
        }
        this.sendMovementToServer(
            transition.dx,
            transition.dy,
            transition.sequence,
        );
    }

    private suspendInput(): void {
        if (this._suspended) return;
        this._suspended = true;

        if (this._currentMovementVector.dx !== 0 || this._currentMovementVector.dy !== 0) {
            // Send STOP synchronously before requestAnimationFrame is throttled.
            this.queueInput({ dx: 0, dy: 0 });
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

        if (this.input.isKeyDown("w")) dy = -1;
        if (this.input.isKeyDown("s")) dy = 1;
        if (this.input.isKeyDown("a")) dx = -1;
        if (this.input.isKeyDown("d")) dx = 1;

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
    private sendMovementToServer(dx: number, dy: number, inputSequence: number): void {
        if (!this._networkManager) return;

        this._networkManager.sendMovement(dx, dy, inputSequence);
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
        this._isMoving = false;
        this._inputSequence = 0;
        this._lastAckSequence = 0;
        this._hasAck = false;
        this._suspended = document.visibilityState === "hidden";

        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
        } else {
            this.position.x = x;
            this.position.y = y;
        }
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
     * Обновить направление спрайта
     */
    updateScale(mouseX: number): void {
        const newDirection = mouseX < this.position.x ? -1 : 1;
        const currentDirection = Math.sign(this.scale.x);

        if (newDirection !== currentDirection) {
            this.scale.x = mouseX < this.position.x ? -Math.abs(this.scale.x) : Math.abs(this.scale.x);

            if (this._networkManager) {
                this._networkManager.sendDirection(newDirection as -1 | 1);
            }
        }
    }
}
