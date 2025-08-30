import { Point } from "pixi.js";
import { InputManager } from "../utils/inputManager";
import { NetworkManager } from "../network/networkManager";
import { AnimationController, PlayerState } from "./animationController";
import { CoordinateConverter } from "../utils/coordinateConverter";
import { MOVEMENT } from "../../common/gameSettings";

/**
 * Контроллер движения для локального игрока с client-side prediction
 *
 * Принцип работы:
 * 1. При нажатии клавиш определяем вектор движения (-1, 0, 1)
 * 2. Отправляем вектор движения на сервер
 * 3. Локально предсказываем движение для плавности (client-side prediction)
 * 4. Сервер рассчитывает позицию и рассылает обновления
 * 5. При получении gameState корректируем позицию (server reconciliation)
 *
 * Client-side prediction + Server reconciliation = плавное движение без лагов!
 * Виртуальный мир: 1000x1000 целочисленных единиц
 * Экран: адаптируется к размеру экрана клиента
 */
export class MovementController {
    private _isMoving = false;
    private _scale: Point;
    private _networkManager: NetworkManager | null = null;
    private _animationController: AnimationController | null = null;
    private _coordinateConverter: CoordinateConverter | null = null;

    private _virtualPosition = { x: 0, y: 0 };

    private _currentMovementVector = { dx: 0, dy: 0 };

    private _inputSequence = 0;
    private _pendingInputs: Array<{sequence: number, dx: number, dy: number, timestamp: number}> = [];

    // Флаг для отслеживания отправки стоп-команды во время атаки
    private _attackStopSent = false;

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
        const moveDistance = MOVEMENT.PLAYER_SPEED_PER_TICK;

        if (dx !== 0) {
            this._virtualPosition.x += dx * moveDistance;
        }
        if (dy !== 0) {
            this._virtualPosition.y += dy * moveDistance;
        }

        if (this._coordinateConverter) {
            const clampedPos = this._coordinateConverter.clampToVirtualBounds(
                this._virtualPosition.x, this._virtualPosition.y
            );
            this._virtualPosition.x = clampedPos.x;
            this._virtualPosition.y = clampedPos.y;
        }

        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.virtualToScreen(this._virtualPosition.x, this._virtualPosition.y);
            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
        }
    }

    /**
     * Обработать acknowledgment от сервера (server authoritative confirmation)
     */
    handleMovementAcknowledgment(acknowledgedPosition: {x: number, y: number}, inputSequence: number): void {
        this._pendingInputs = this._pendingInputs.filter(input => input.sequence !== inputSequence);

        const deltaX = acknowledgedPosition.x - this._virtualPosition.x;
        const deltaY = acknowledgedPosition.y - this._virtualPosition.y;
        const distanceSquared = deltaX * deltaX + deltaY * deltaY;

        // Simple correction: either position is accurate or we reconcile
        if (distanceSquared > 16) { // 4 pixels tolerance - if bigger difference, reconcile
            this.reconcilePosition(acknowledgedPosition, inputSequence);
        }
        // If distanceSquared <= 16, client prediction is accurate enough
    }

    /**
     * Пересчет позиции на основе server acknowledgment (server reconciliation)
     */
    private reconcilePosition(serverPosition: {x: number, y: number}, lastAckedSequence: number): void {
        // Set position to server authoritative position
        this._virtualPosition.x = serverPosition.x;
        this._virtualPosition.y = serverPosition.y;

        // Re-apply pending inputs that happened after the acknowledged sequence
        const futureInputs = this._pendingInputs.filter(input => input.sequence > lastAckedSequence);

        if (futureInputs.length > 0) {
            for (const input of futureInputs) {
                const moveDistance = MOVEMENT.PLAYER_SPEED_PER_TICK;
                this._virtualPosition.x += input.dx * moveDistance;
                this._virtualPosition.y += input.dy * moveDistance;

                if (this._coordinateConverter) {
                    const clampedPos = this._coordinateConverter.clampToVirtualBounds(
                        this._virtualPosition.x, this._virtualPosition.y
                    );
                    this._virtualPosition.x = clampedPos.x;
                    this._virtualPosition.y = clampedPos.y;
                }
            }
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
        if (this._animationController && this._animationController.playerState === PlayerState.ATTACKING) {
            // During attack, send stop only once at the beginning
            this._isMoving = false;

            if (!this._attackStopSent) {
                this._inputSequence++;
                this._pendingInputs.push({
                    sequence: this._inputSequence,
                    dx: 0,
                    dy: 0,
                    timestamp: Date.now()
                });

                if (this._pendingInputs.length > 10) {
                    this._pendingInputs.shift();
                }

                this.sendMovementToServer(0, 0, this._inputSequence);
                this._attackStopSent = true;
            }

            return false;
        }

        // Reset attack stop flag when not attacking
        this._attackStopSent = false;

        const desiredVector = this.getDesiredMovementVector();

        if (desiredVector.dx !== 0 || desiredVector.dy !== 0) {
            this._isMoving = true;

            // Always update movement vector
            this._currentMovementVector = desiredVector;

            // Send movement to server every tick
            const now = Date.now();
            this._inputSequence++;

            this._pendingInputs.push({
                sequence: this._inputSequence,
                dx: desiredVector.dx,
                dy: desiredVector.dy,
                timestamp: now
            });

            if (this._pendingInputs.length > 10) {
                this._pendingInputs.shift();
            }

            // Apply movement first to get new position
            this.applyMovement(this._currentMovementVector.dx, this._currentMovementVector.dy);

            // Then send movement with the NEW position (after movement)
            this.sendMovementToServer(desiredVector.dx, desiredVector.dy, this._inputSequence);

            return true;
        } else {
            this._isMoving = false;

            if (this.vectorChanged(desiredVector)) {
                this._currentMovementVector = desiredVector;

                this._inputSequence++;

                this._pendingInputs.push({
                    sequence: this._inputSequence,
                    dx: desiredVector.dx,
                    dy: desiredVector.dy,
                    timestamp: Date.now()
                });

                // Send movement with current position (no movement applied yet for stop)
                this.sendMovementToServer(desiredVector.dx, desiredVector.dy, this._inputSequence);
            }

            return false;
        }
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
    private vectorChanged(newVector: { dx: number; dy: number }): boolean {
        return newVector.dx !== this._currentMovementVector.dx ||
               newVector.dy !== this._currentMovementVector.dy;
    }

    /**
     * Отправить движение на сервер
     */
    private sendMovementToServer(dx: number, dy: number, inputSequence: number): void {
        if (!this._networkManager) return;

        // Send current position to server for validation
        const currentPosition = this.getVirtualPosition();
        this._networkManager.sendMovement(dx, dy, inputSequence, currentPosition);
    }

    /**
     * Установить начальную позицию
     */
    public setInitialPosition(x: number, y: number): void {
        this._virtualPosition.x = Math.round(x);
        this._virtualPosition.y = Math.round(y);

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