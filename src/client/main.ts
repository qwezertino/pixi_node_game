import { Application, Container, Graphics } from "pixi.js";
import { SpriteLoader } from "./utils/spriteLoader";
import { FpsDisplay } from "./utils/fpsDisplay";
import { InputManager } from "./utils/inputManager";
import { MovementController } from "./controllers/movementController";
import { AnimationController, PlayerState } from "./controllers/animationController";
import { NetworkManager } from "./network/networkManager";
import { PlayerManager } from "./game/playerManager";
import { TICK_RATE } from "../protocol/messages";
import { PLAYER, COLORS } from "../shared/gameConfig";
import { BinaryProtocol } from "../protocol/binaryProtocol";
import { CoordinateConverter } from "./utils/coordinateConverter";

(async () => {
    // Check for WebGL support and detect software rendering
    const canvas = document.createElement('canvas');
    const gl = canvas.getContext('webgl') || canvas.getContext('experimental-webgl');

    let useCanvas = false;
    if (!gl || !(gl instanceof WebGLRenderingContext)) {
        console.warn('WebGL not supported, falling back to Canvas renderer');
        useCanvas = true;
    } else {
        // Check if using software renderer
        const debugInfo = gl.getExtension('WEBGL_debug_renderer_info');
        if (debugInfo) {
            const renderer = gl.getParameter(debugInfo.UNMASKED_RENDERER_WEBGL);
            console.log('WebGL Renderer:', renderer);

            // Check for software renderers
            if (renderer && (
                renderer.includes('Software') ||
                renderer.includes('SwiftShader') ||
                renderer.includes('Mesa') ||
                renderer.includes('llvmpipe') ||
                renderer.toLowerCase().includes('software')
            )) {
                console.warn('Software WebGL detected, falling back to Canvas for better performance');
                useCanvas = true;
            }
        } else {
            console.warn('Cannot detect WebGL renderer, assuming hardware acceleration');
        }
    }

    const app = new Application();

    if (useCanvas) {
        // For Canvas renderer, create canvas manually
        const canvasElement = document.createElement('canvas');
        canvasElement.width = window.innerWidth;
        canvasElement.height = window.innerHeight;
        await app.init({
            background: "#202020ff",
            resizeTo: window,
            eventMode: "static",
            antialias: true,
            canvas: canvasElement
        });
        console.log('Using Canvas renderer');
    } else {
        await app.init({
            background: "#202020ff",
            resizeTo: window,
            eventMode: "static",
            antialias: true
        });
        console.log('Using WebGL renderer');
    }

    const container = document.getElementById("pixi-container")!;
    container.appendChild(app.canvas);

    // Create world background (light gray) to visualize world boundaries
    const worldBackground = new Graphics();
    worldBackground.rect(0, 0, app.screen.width, app.screen.height);
    worldBackground.fill(parseInt(COLORS.worldBackground.replace('#', ''), 16)); // World background color from config
    app.stage.addChild(worldBackground);

    // Create player container for organizing all player sprites
    const playerContainer = new Container();
    app.stage.addChild(playerContainer);

    // Initialize modules
    const input = new InputManager(app.canvas);

    // Create NetworkManager but don't connect yet
    const networkManager = new NetworkManager();

    // Initialize FPS display with network manager
    const fpsDisplay = new FpsDisplay(app, networkManager);

    // Connect FPS display to network manager for ping tracking
    networkManager.setFpsDisplay(fpsDisplay);

    // Set up F3 key to toggle detailed stats
    input.setF3Callback(() => {
        fpsDisplay.toggleDetailedStats();
    });

    // Setup coordinate converter for virtual world coordinates
    // Используем реальные размеры экрана приложения
    const coordinateConverter = new CoordinateConverter(app.screen.width, app.screen.height);

    // Setup player manager BEFORE connecting to handle initialState
    const playerManager = new PlayerManager(playerContainer, networkManager, coordinateConverter);

    // Setup player
    const characterVisual = await SpriteLoader.loadCharacterVisual("/assets/16x16_knight_3_v3.png");

    const playerSprite = characterVisual.getAnimation("idle")!;
    playerSprite.scale.set(PLAYER.baseScale); // Используем настройки из gameSettings
    playerSprite.animationSpeed = PLAYER.animationSpeed; // Используем настройки из gameSettings
    playerSprite.play();

    // Set initial player position at virtual world center (will be updated when connected to server)
    const virtualCenter = coordinateConverter.getVirtualCenter();
    const screenCenter = coordinateConverter.virtualToScreen(virtualCenter.x, virtualCenter.y);
    playerSprite.position.set(screenCenter.x, screenCenter.y);
    playerContainer.addChild(playerSprite);

    const animationController = new AnimationController(characterVisual.animations, playerSprite);
    const movementController = new MovementController(input, playerSprite.position, playerSprite.scale);

    // Обработчик начала атаки - сообщаем MovementController
    animationController.onAttackStart(() => {
        movementController.setAttackStarted();
    });

    // Обработчик окончания атаки - отправляем текущее состояние движения на сервер
    animationController.onAttackEnd(() => {
        movementController.onAttackEnd();
        networkManager.sendAttackEnd();
    });

    // Connect movement controller to network manager, animation controller, and coordinate converter
    movementController.setNetworkManager(networkManager);
    movementController.setAnimationController(animationController);
    movementController.setCoordinateConverter(coordinateConverter);

    // Connect player manager to movement controller for server corrections
    playerManager.setMovementController(movementController);

    // Connect network manager to movement controller for movement acknowledgments
    networkManager.onMovementAck((position, inputSequence) => {
        movementController.handleMovementAcknowledgment(position, inputSequence);
    });

    // Обработчик изменения размеров окна
    const handleResize = () => {
        const newWidth = app.screen.width;
        const newHeight = app.screen.height;

        // Обновляем размеры в coordinate converter
        coordinateConverter.updateScreenSize(newWidth, newHeight);

        // Обновляем размер фона мира
        worldBackground.clear();
        worldBackground.rect(0, 0, newWidth, newHeight);
        worldBackground.fill(parseInt(COLORS.worldBackground.replace('#', ''), 16));

        // Обновляем позиции всех игроков
        playerManager.updateAllPlayerPositions();
    };

    // Добавляем обработчик изменения размеров окна
    window.addEventListener('resize', handleResize);


    // Wait for network connection and initial position
    console.log("Waiting for network connection and player ID...");
    await new Promise<void>((resolve) => {
        const checkInterval = setInterval(() => {
            // If we have a player ID, we're connected
            const playerId = networkManager.getPlayerId();
            if (playerId) {
                clearInterval(checkInterval);
                console.log("Got player ID:", playerId);

                                // Set player position from server (server sends world coordinates)
                const initialPosition = networkManager.getInitialPosition();
                console.log("Initial position:", initialPosition);

                // Only set the movement controller - it will handle coordinate conversion
                movementController.setInitialPosition(initialPosition.x, initialPosition.y);

                resolve();
            }
        }, 100);
    });
    console.log("Network connection established, starting game...");

        // Attack handling with immediate feedback
    app.canvas.addEventListener("mousedown", (e) => {
        if (e.button === 0 && animationController.handleAttack()) {
            // Immediate visual feedback - play attack animation instantly
            animationController.setAnimation("attack");

            // Send attack to server in binary format
            const position = { x: playerSprite.position.x, y: playerSprite.position.y };
            const attackMsg = {
                type: 'attack' as const,
                position
            };

            // Use binary protocol for attack
            const binaryData = BinaryProtocol.encodeAttack(attackMsg);
            networkManager.sendAttack(binaryData);
        }
    });

    // Fixed timestep for physics updates
    const fixedTimeStep = 1 / TICK_RATE;
    let accumulator = 0;

    // Game loop
    console.log("Starting game loop...");
    app.ticker.add((time) => {
        const deltaTime = time.deltaTime / 60; // Convert to seconds
        // Update FPS display
        fpsDisplay.update();

        // Accumulate time
        accumulator += deltaTime;

        // Process physics at fixed time steps
        while (accumulator >= fixedTimeStep) {
            // Always update movement - let MovementController decide attack behavior
            const isMoving = movementController.update(fixedTimeStep);
            movementController.updateScale(input.mousePosition.x);

            // Set animation state based on attack state and movement
            if (animationController.playerState !== PlayerState.ATTACKING) {
                animationController.setState(isMoving ? PlayerState.MOVING : PlayerState.IDLE);
            }
            // During attack, keep ATTACKING state and don't change it

            // Update remote players
            playerManager.update(fixedTimeStep);

            // Decrease accumulated time
            accumulator -= fixedTimeStep;
        }

        // Update animation and visual state (can run at variable frame rate)
        animationController.playerRef.scale.copyFrom(movementController.scale);
    });
    console.log("Game loop started");
})();
