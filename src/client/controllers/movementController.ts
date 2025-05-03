import { Point } from "pixi.js";
import { InputManager } from "../utils/inputManager";
import { NetworkManager } from "../network/networkManager";
import { TICK_RATE } from "../../protocol/messages";
import { MOVEMENT } from "../../common/gameSettings";

export class MovementController {
    private _speed = MOVEMENT.PLAYER_SPEED; // Используем настройку скорости из gameSettings
    private _isMoving = false;
    private _scale: Point;
    private _networkManager: NetworkManager | null = null;
    private _lastMovementVector = { dx: 0, dy: 0 };

    get isMoving() {
        return this._isMoving;
    }

    get scale() {
        return this._scale;
    }

    constructor(
        private input: InputManager,
        private position: Point,
        scale: Point,
    ) {
        this._scale = scale;
    }

    // Set network manager to enable server communication
    setNetworkManager(networkManager: NetworkManager): void {
        this._networkManager = networkManager;

        // Listen for position corrections from server
        this._networkManager.onCorrection((correctedPosition) => {
            this.position.x = correctedPosition.x;
            this.position.y = correctedPosition.y;
        });
    }

    update(deltaTime: number) {
        this._isMoving = false;
        let movementX = 0;
        let movementY = 0;

        if (this.input.isKeyDown("w")) movementY -= 1;
        if (this.input.isKeyDown("s")) movementY += 1;
        if (this.input.isKeyDown("a")) movementX -= 1;
        if (this.input.isKeyDown("d")) movementX += 1;

        const totalMovement = Math.sqrt(movementX ** 2 + movementY ** 2);

        // Calculate normalized movement vector
        if (totalMovement > 0) {
            const normalizedX = movementX / totalMovement;
            const normalizedY = movementY / totalMovement;

            // Use fixed time step instead of variable deltaTime
            // We use a fixed value (1 / TICK_RATE) to match server's physics update
            const fixedTimeStep = 1 / TICK_RATE;

            // Apply movement locally for immediate feedback
            this.position.x += normalizedX * this._speed * fixedTimeStep;
            this.position.y += normalizedY * this._speed * fixedTimeStep;

            this._isMoving = true;

            // Only send movement update if vector changed
            if (normalizedX !== this._lastMovementVector.dx ||
                normalizedY !== this._lastMovementVector.dy) {

                this._lastMovementVector.dx = normalizedX;
                this._lastMovementVector.dy = normalizedY;

                // Send movement to server if network manager exists
                if (this._networkManager) {
                    this._networkManager.sendMovement(normalizedX, normalizedY);
                }
            }
        } else if (this._lastMovementVector.dx !== 0 || this._lastMovementVector.dy !== 0) {
            // Send stop movement to server
            this._lastMovementVector.dx = 0;
            this._lastMovementVector.dy = 0;
            this._isMoving = false;

            if (this._networkManager) {
                this._networkManager.sendMovement(0, 0);
            }
        }

        return this._isMoving;
    }

    updateScale(mouseX: number) {
        const newDirection = mouseX < this.position.x ? -1 : 1;
        const currentDirection = Math.sign(this.scale.x);

        // Only update if direction changed
        if (newDirection !== currentDirection) {
            this.scale.x = mouseX < this.position.x ? -Math.abs(this.scale.x) : Math.abs(this.scale.x);

            // Send direction change to server
            if (this._networkManager) {
                this._networkManager.sendDirection(newDirection as -1 | 1);
            }
        }
    }
}