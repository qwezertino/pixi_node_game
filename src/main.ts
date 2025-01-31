
import { Application } from "pixi.js";
import { SpriteLoader } from "./spriteLoader";
import { FpsDisplay } from "./utils/fpsDisplay";
import { InputManager } from "./utils/inputManager";
import { MovementController } from "./controllers/movementController";
import { AnimationController, PlayerState } from "./controllers/animationController";

(async () => {
    const app = new Application();
    await app.init({
        background: "#1099bb",
        resizeTo: window,
        eventMode: "static",
        antialias: true
    });

    const container = document.getElementById("pixi-container")!;
    container.appendChild(app.canvas);

    // Initialize modules
    const fpsDisplay = new FpsDisplay(app);
    const input = new InputManager(app.canvas);
    const characterVisual = await SpriteLoader.loadCharacterVisual("/assets/16x16_knight_3_v3.png");

    // Setup player
    const baseScale = 2;
    const playerSprite = characterVisual.getAnimation("idle")!;
    playerSprite.position.set(app.screen.width / 2, app.screen.height / 2);
    playerSprite.scale.set(baseScale);
    playerSprite.animationSpeed = 0.1;
    playerSprite.play();
    app.stage.addChild(playerSprite);

    const animationController = new AnimationController(characterVisual.animations, playerSprite);
    const movementController = new MovementController(input, playerSprite.position, playerSprite.scale);

    // Attack handling
    app.canvas.addEventListener("mousedown", (e) => {
        if (e.button === 0 && animationController.handleAttack()) {
            animationController.setAnimation("attack");
        }
    });

    // Game loop
    app.ticker.add((time) => {
        fpsDisplay.update();

        const deltaTime = time.deltaTime;
        if (animationController.playerState === PlayerState.ATTACKING) {
            return;
        }
        const isMoving = movementController.update(deltaTime);
        movementController.updateScale(input.mousePosition.x);
        animationController.setState(isMoving ? PlayerState.MOVING : PlayerState.IDLE);

        // Update scale reference for mouse flipping
        animationController.playerRef.scale.copyFrom(movementController.scale);
    });
})();
