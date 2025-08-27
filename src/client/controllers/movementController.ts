import { Point } from "pixi.js";
import { InputManager } from "../utils/inputManager";
import { NetworkManager } from "../network/networkManager";
import { TICK_RATE } from "../../protocol/messages";
import { AnimationController, PlayerState } from "./animationController";
import { CoordinateConverter } from "../utils/coordinateConverter";
import { MOVEMENT } from "../../common/gameSettings";

export class MovementController {
    private _isMoving = false;
    private _scale: Point;
    private _networkManager: NetworkManager | null = null;
    private _animationController: AnimationController | null = null;
    private _lastMovementVector = { dx: 0, dy: 0 };
    private _coordinateConverter: CoordinateConverter | null = null;
    private _worldPosition: Point = new Point(0, 0); // Position in world coordinates

    // Client-side prediction state
    private _predictedPosition: Point;
    private _confirmedPosition: Point;
    private _positionHistory: Array<{ position: Point; timestamp: number; inputId: number }> = [];
    private _currentInputId = 0;
    private _predictionFrames = 0; // Track prediction frames since last send
    private _correctionSmoothing = 0.1; // How fast to correct prediction errors

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
        this._predictedPosition = new Point(position.x, position.y);
        this._confirmedPosition = new Point(position.x, position.y);
    }

    // Set network manager to enable server communication
    setNetworkManager(networkManager: NetworkManager): void {
        this._networkManager = networkManager;

        // Listen for position corrections from server
        this._networkManager.onCorrection((correctedPosition) => {
            this.handleServerCorrection(correctedPosition);
        });
    }

        setAnimationController(animationController: AnimationController): void {
        this._animationController = animationController;

        // Listen for attack end to re-send current movement state
        animationController.onAttackEnd(() => {
            this.resendCurrentMovement();
        });
    }

    setCoordinateConverter(converter: CoordinateConverter): void {
        this._coordinateConverter = converter;
    }

    private resendCurrentMovement(): void {
        // Check current input state and force send to server
        let movementX = 0;
        let movementY = 0;

        if (this.input.isKeyDown("w")) movementY -= 1;
        if (this.input.isKeyDown("s")) movementY += 1;
        if (this.input.isKeyDown("a")) movementX -= 1;
        if (this.input.isKeyDown("d")) movementX += 1;

        const totalMovement = Math.sqrt(movementX ** 2 + movementY ** 2);

        // Note: Movement sending is handled in the main update() logic
    }

                update(_deltaTime: number) {
        this._isMoving = false;
        let movementX = 0;
        let movementY = 0;

        // Don't process movement during attacks
        if (this._animationController && this._animationController.playerState === PlayerState.ATTACKING) {
            return;
        }

        if (this.input.isKeyDown("w")) movementY -= 1;
        if (this.input.isKeyDown("s")) movementY += 1;
        if (this.input.isKeyDown("a")) movementX -= 1;
        if (this.input.isKeyDown("d")) movementX += 1;

        // Use INTEGER movement vectors everywhere for consistency
        if (movementX !== 0 || movementY !== 0) {
            // Use fixed time step instead of variable deltaTime
            const fixedTimeStep = 1 / TICK_RATE;

            // Apply movement with client-side prediction using INTEGER values
            // This matches what remote players receive from server
            // BUT only if we haven't hit prediction limit
            if (this._predictionFrames < 3) {
                this.applyMovementPrediction(movementX, movementY, fixedTimeStep);
                this._predictionFrames++; // Count this prediction frame
                console.log(`🎮 [CLIENT] New movement prediction: frame ${this._predictionFrames}/3`);
                this._isMoving = true;
            } else {
                console.log(`🚫 [CLIENT] Movement blocked - prediction limit reached (${this._predictionFrames}/3)`);
                // Don't set _isMoving = true if we're at prediction limit
                // This prevents further movement until server responds
            }

            // Send INTEGER directions to server for network protocol
            if (movementX !== this._lastMovementVector.dx ||
                movementY !== this._lastMovementVector.dy) {

                this._lastMovementVector.dx = movementX;  // Store raw integers
                this._lastMovementVector.dy = movementY;

                // Send INTEGER movement to server
                if (this._networkManager) {
                    this._currentInputId++;
                    this._predictionFrames = 0; // Reset prediction counter when sending
                    this._networkManager.sendMovement(movementX, movementY);

                    // Store position for reconciliation
                    this.storePositionHistory();
                }
            } else {
                // No movement change, but continue prediction if already moving
                // LIMIT: Only predict for a few frames to prevent massive desync
                if (this._isMoving && this._predictionFrames < 3) {
                    // Apply movement with client-side prediction (limited)
                    this.applyMovementPrediction(this._lastMovementVector.dx, this._lastMovementVector.dy, fixedTimeStep);
                    this._predictionFrames++; // Increment prediction counter
                    console.log(`🔮 [CLIENT] Continue prediction: frame ${this._predictionFrames}/3`);
                } else if (this._isMoving) {
                    console.log(`⏸️ [CLIENT] Prediction limit reached, waiting for server`);
                    // FORCE STOP movement to prevent further prediction
                    this._isMoving = false;
                    console.log(`🛑 [CLIENT] FORCED STOP - prediction limit enforced`);
                }
            }
        } else if (this._lastMovementVector.dx !== 0 || this._lastMovementVector.dy !== 0) {
            // Send stop movement to server AND stop local prediction immediately
            this._lastMovementVector.dx = 0;
            this._lastMovementVector.dy = 0;
            this._isMoving = false;

            console.log(`⏸️ [CLIENT] Stopping movement prediction immediately`);

            if (this._networkManager) {
                this._currentInputId++;
                this._predictionFrames = 0; // Reset prediction counter when stopping
                this._networkManager.sendMovement(0, 0);
                this.storePositionHistory();
            }

            // CRITICAL: Return immediately to prevent any further processing
            return this._isMoving;
        }

        // Smooth interpolation between predicted and confirmed positions
        this.interpolatePosition();

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

    private applyMovementPrediction(dx: number, dy: number, fixedTimeStep: number) {
        // Use virtual world units for consistency with server
        const moveDistance = MOVEMENT.PLAYER_SPEED * fixedTimeStep;
        const prevWorldX = this._worldPosition.x;
        const prevWorldY = this._worldPosition.y;

        console.log(`🎮 [CLIENT] Movement prediction: dx=${dx}, dy=${dy}, moveDistance=${moveDistance.toFixed(3)}, fixedTimeStep=${fixedTimeStep.toFixed(4)}`);

        // Update world position
        if (dx !== 0) {
            this._worldPosition.x += (dx > 0 ? moveDistance : -moveDistance);
        }
        if (dy !== 0) {
            this._worldPosition.y += (dy > 0 ? moveDistance : -moveDistance);
        }

        // Round to discrete positions to match server
        this._worldPosition.x = Math.round(this._worldPosition.x);
        this._worldPosition.y = Math.round(this._worldPosition.y);

        console.log(`🌍 [CLIENT] World position changed: from (${prevWorldX.toFixed(2)}, ${prevWorldY.toFixed(2)}) to discrete (${this._worldPosition.x}, ${this._worldPosition.y})`);

        // Convert world position to screen coordinates for sprite
        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.worldToScreen(this._worldPosition.x, this._worldPosition.y);
            this._predictedPosition.x = screenPos.x;
            this._predictedPosition.y = screenPos.y;

            console.log(`📺 [CLIENT] Screen position: (${screenPos.x.toFixed(2)}, ${screenPos.y.toFixed(2)})`);

            // Update sprite position immediately for responsive controls
            this.position.x = this._predictedPosition.x;
            this.position.y = this._predictedPosition.y;
        }
    }

    private storePositionHistory() {
        const historyEntry = {
            position: new Point(this._predictedPosition.x, this._predictedPosition.y),
            timestamp: Date.now(),
            inputId: this._currentInputId
        };

        this._positionHistory.push(historyEntry);

        // Limit history size to prevent memory issues (keep last 60 entries ~1 second at 60fps)
        if (this._positionHistory.length > 60) {
            this._positionHistory.shift();
        }
    }

    private handleServerCorrection(correctedPosition: { x: number; y: number }) {
        // Round server position to discrete coordinates
        const discreteServerX = Math.round(correctedPosition.x);
        const discreteServerY = Math.round(correctedPosition.y);

        // Calculate difference between client world position and server position
        const deltaX = discreteServerX - this._worldPosition.x;
        const deltaY = discreteServerY - this._worldPosition.y;
        const distance = Math.sqrt(deltaX * deltaX + deltaY * deltaY);

        console.log(`🔄 [CLIENT] Server correction: client world (${this._worldPosition.x}, ${this._worldPosition.y}) -> server discrete (${discreteServerX}, ${discreteServerY}), distance: ${distance.toFixed(2)}`);

                // AGGRESSIVE SERVER AUTHORITY: Always trust server for perfect sync
        if (distance > 0) {
            console.log(`⚡ [CLIENT] Server correction: ${distance} units - trusting server completely`);
            this._worldPosition.x = discreteServerX;
            this._worldPosition.y = discreteServerY;

            // Reset prediction frames since we got server update
            this._predictionFrames = 0;

            // Update screen position based on corrected world position
            if (this._coordinateConverter) {
                const screenPos = this._coordinateConverter.worldToScreen(this._worldPosition.x, this._worldPosition.y);
                console.log(`📺 [CLIENT] Server authoritative position: (${screenPos.x.toFixed(2)}, ${screenPos.y.toFixed(2)})`);
            }

            // If server says we're stopped, stop immediately
            if (distance > 5) {
                this._isMoving = false;
                this._lastMovementVector = { dx: 0, dy: 0 };
                console.log(`⏸️ [CLIENT] Server correction forced stop`);
            }
        }

        // Also update legacy predicted/confirmed positions for compatibility
        this._confirmedPosition.x = discreteServerX;
        this._confirmedPosition.y = discreteServerY;
        this._predictedPosition.x = discreteServerX;
        this._predictedPosition.y = discreteServerY;
    }

    private interpolatePosition() {
        // Smoothly interpolate between predicted and confirmed positions
        // This helps smooth out any jitter from corrections
        const lerpFactor = this._correctionSmoothing;

        this.position.x = this._predictedPosition.x * (1 - lerpFactor) + this._confirmedPosition.x * lerpFactor;
        this.position.y = this._predictedPosition.y * (1 - lerpFactor) + this._confirmedPosition.y * lerpFactor;
    }

        // Method to set initial position (called when connecting to server)
    public setInitialPosition(x: number, y: number) {
        console.log(`🎯 [CLIENT] Setting initial position: server world=(${x.toFixed(2)}, ${y.toFixed(2)})`);

        // Round to discrete coordinates to match server system
        const discreteX = Math.round(x);
        const discreteY = Math.round(y);
        console.log(`🎯 [CLIENT] Rounded to discrete: (${discreteX}, ${discreteY})`);

        // Server sends world coordinates, store them and convert to screen
        this._worldPosition.x = discreteX;
        this._worldPosition.y = discreteY;

        if (this._coordinateConverter) {
            const screenPos = this._coordinateConverter.worldToScreen(discreteX, discreteY);
            console.log(`📺 [CLIENT] Initial screen position: discrete world (${discreteX}, ${discreteY}) -> screen (${screenPos.x.toFixed(2)}, ${screenPos.y.toFixed(2)})`);

            this.position.x = screenPos.x;
            this.position.y = screenPos.y;
            this._predictedPosition.x = screenPos.x;
            this._predictedPosition.y = screenPos.y;
            this._confirmedPosition.x = screenPos.x;
            this._confirmedPosition.y = screenPos.y;
        } else {
            // Fallback if no converter set yet
            console.log(`⚠️ [CLIENT] No coordinate converter, using raw discrete coordinates (${discreteX}, ${discreteY})`);
            this.position.x = discreteX;
            this.position.y = discreteY;
            this._predictedPosition.x = discreteX;
            this._predictedPosition.y = discreteY;
            this._confirmedPosition.x = discreteX;
            this._confirmedPosition.y = discreteY;
        }
    }
}