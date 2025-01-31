import { Point } from "pixi.js";
import { InputManager } from "../utils/inputManager";

export class MovementController {
    private _speed = 5;

    private _isMoving = false;
    private _scale: Point;
    get isMoving() {
        return this._isMoving;
    }

    get scale() {
        return this._scale;
    }

    // Update references in the class to use these private properties
    constructor(
        private input: InputManager,
        private position: Point,
        scale: Point,
    ) {
        this._scale = scale;
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
        if (totalMovement > 0) {
            const normalizedX = movementX / totalMovement;
            const normalizedY = movementY / totalMovement;

            this.position.x += normalizedX * this._speed * deltaTime;
            this.position.y += normalizedY * this._speed * deltaTime;
            this._isMoving = true;
        } else {
            this._isMoving = false;
        }

        return this._isMoving;
    }

    updateScale(mouseX: number) {
        this.scale.x = mouseX < this.position.x ? -Math.abs(this.scale.x) : Math.abs(this.scale.x);
    }
}