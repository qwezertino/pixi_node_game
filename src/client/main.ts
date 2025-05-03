import { Application, Container } from "pixi.js";
import { SpriteLoader } from "./utils/spriteLoader";
import { FpsDisplay } from "./utils/fpsDisplay";
import { InputManager } from "./utils/inputManager";
import { MovementController } from "./controllers/movementController";
import { AnimationController, PlayerState } from "./controllers/animationController";
import { NetworkManager } from "./network/networkManager";
import { PlayerManager } from "./game/playerManager";
import { TICK_RATE } from "../protocol/messages";
import { PLAYER } from "../common/gameSettings";

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

    // Create player container for organizing all player sprites
    const playerContainer = new Container();
    app.stage.addChild(playerContainer);

    // Initialize modules
    const fpsDisplay = new FpsDisplay(app);
    const input = new InputManager(app.canvas);
    const networkManager = new NetworkManager();

    // Setup player
    const characterVisual = await SpriteLoader.loadCharacterVisual("/assets/16x16_knight_3_v3.png");

    const playerSprite = characterVisual.getAnimation("idle")!;
    playerSprite.scale.set(PLAYER.BASE_SCALE); // Используем настройки из gameSettings
    playerSprite.animationSpeed = PLAYER.ANIMATION_SPEED; // Используем настройки из gameSettings
    playerSprite.play();

    // Set initial player position (will be updated when connected to server)
    playerSprite.position.set(app.screen.width / 2, app.screen.height / 2);
    playerContainer.addChild(playerSprite);

    const animationController = new AnimationController(characterVisual.animations, playerSprite);
    const movementController = new MovementController(input, playerSprite.position, playerSprite.scale);

    // Connect movement controller to network manager
    movementController.setNetworkManager(networkManager);

    // Setup player manager to handle other players
    const playerManager = new PlayerManager(playerContainer, networkManager);

    // Wait for network connection and initial position
    await new Promise<void>((resolve) => {
        const checkInterval = setInterval(() => {
            // If we have a player ID, we're connected
            if (networkManager.getPlayerId()) {
                clearInterval(checkInterval);

                // Set player position from server
                const initialPosition = networkManager.getInitialPosition();
                playerSprite.position.set(initialPosition.x, initialPosition.y);

                resolve();
            }
        }, 100);
    });

    // Attack handling
    app.canvas.addEventListener("mousedown", (e) => {
        if (e.button === 0 && animationController.handleAttack()) {
            console.log("Attacking");
            animationController.setAnimation("attack");

            // Send attack to server as JSON (attacks are less frequent)
            const position = { x: playerSprite.position.x, y: playerSprite.position.y };
            const attackMsg = JSON.stringify({
                type: "attack",
                position
            });

            // We could switch to binary protocol later if needed
        }
    });

    // Fixed timestep for physics updates
    const fixedTimeStep = 1 / TICK_RATE;
    let accumulator = 0;

    // Game loop
    app.ticker.add((time) => {
        const deltaTime = time.deltaTime / 60; // Convert to seconds

        // Update FPS display
        fpsDisplay.update();

        // Accumulate time
        accumulator += deltaTime;

        // Process physics at fixed time steps
        while (accumulator >= fixedTimeStep) {
            // Update movement only if not attacking
            if (animationController.playerState !== PlayerState.ATTACKING) {
                const isMoving = movementController.update(fixedTimeStep);
                movementController.updateScale(input.mousePosition.x);
                animationController.setState(isMoving ? PlayerState.MOVING : PlayerState.IDLE);
            }

            // Update remote players
            playerManager.update(fixedTimeStep);

            // Decrease accumulated time
            accumulator -= fixedTimeStep;
        }

        // Update animation and visual state (can run at variable frame rate)
        animationController.playerRef.scale.copyFrom(movementController.scale);
    });
})();
