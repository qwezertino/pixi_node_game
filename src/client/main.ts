import { Application, Container, Graphics } from "pixi.js";
import { SpriteLoader } from "./utils/spriteLoader";
import { FpsDisplay } from "./utils/fpsDisplay";
import { InputManager } from "./utils/inputManager";
import { MovementController } from "./controllers/movementController";
import { AnimationController, PlayerState } from "./controllers/animationController";
import { NetworkManager } from "./network/networkManager";
import { PlayerManager } from "./game/playerManager";
import { TICK_RATE } from "./network/protocol/messages";
import { COLORS } from "../shared/gameConfig";
import { BinaryProtocol } from "./network/protocol/binaryProtocol";
import { CoordinateConverter } from "./utils/coordinateConverter";
import { mountUnitViewerToggle } from "./debug/unitViewerPanel";
import { showUnitSelectScreen } from "./ui/unitSelectScreen";
import { StatusBarWidget } from "./ui/statusBar";
import { StaminaPredictor } from "./utils/staminaPredictor";

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
    playerContainer.eventMode = "none";
    playerContainer.interactiveChildren = false;
    app.stage.addChild(playerContainer);

    // Initialize modules
    const input = new InputManager(app.canvas);

    // Create NetworkManager but don't connect yet
    const networkManager = new NetworkManager();

    // Initialize FPS display with network manager
    const fpsDisplay = new FpsDisplay(app, networkManager);

    // Set up F3 key to toggle detailed stats
    input.setF3Callback(() => {
        fpsDisplay.toggleDetailedStats();
    });

    // Dev tool: floating button to inspect any unit's spritesheet row layout
    // (see debug/unitViewerPanel.ts). Not gated behind a debug flag — cheap to
    // leave on since the panel itself only builds on first click.
    mountUnitViewerToggle();

    // Setup coordinate converter for virtual world coordinates
    // Используем реальные размеры экрана приложения
    const coordinateConverter = new CoordinateConverter(app.screen.width, app.screen.height);

    // Setup player manager BEFORE connecting to handle initialState
    const playerManager = new PlayerManager(playerContainer, networkManager, coordinateConverter);

    // Character select (GDD §54-adjacent UI, no server/network activity yet) — the
    // server never spawns anything until the player has actually chosen a unit.
    const localUnit = await showUnitSelectScreen(app);
    networkManager.connect(localUnit.id);

    // The real per-unit spritesheet is tried first (see spriteLoader.ts /
    // animationLayout.ts); any unit whose sheet doesn't decode cleanly yet falls
    // back to the placeholder knight rather than staying invisible. Already
    // resolved and cached from the select screen, so this is instant.
    const characterVisual = await SpriteLoader.loadUnitCharacterVisual(localUnit).catch(() =>
        SpriteLoader.loadCharacterVisual("/assets/16x16_knight_3_v3.png")
    );

    const playerSprite = characterVisual.getAnimation(characterVisual.directional ? "idle_right" : "idle")!;
    playerSprite.scale.set(characterVisual.scale);
    playerSprite.animationSpeed = localUnit.animationSpeed;
    playerSprite.play();

    // Set initial player position at virtual world center (will be updated when connected to server)
    const virtualCenter = coordinateConverter.getVirtualCenter();
    const screenCenter = coordinateConverter.virtualToScreen(virtualCenter.x, virtualCenter.y);
    playerSprite.position.set(screenCenter.x, screenCenter.y);
    playerContainer.addChild(playerSprite);

    const localStatusBar = new StatusBarWidget();
    playerContainer.addChild(localStatusBar.container);

    // Predicts stamina locally every frame (see staminaPredictor.ts) instead of
    // waiting on the low-frequency UNIT_ROSTER channel — corrected whenever a real
    // roster value arrives, the same "predict, then correct" shape as movement.
    const staminaPredictor = new StaminaPredictor(localUnit);
    networkManager.onUnitRoster((entries) => {
        const own = entries[networkManager.getPlayerId()];
        if (own) staminaPredictor.reconcile(own.stamina);
    });

    const animationController = new AnimationController(characterVisual, playerSprite);
    const movementController = new MovementController(input, playerSprite.position, playerSprite.scale);
    movementController.setStaminaProvider(() => staminaPredictor.current);
    movementController.setBlockingProvider(() => networkManager.getPlayers()[networkManager.getPlayerId()]?.blocking ?? false);
    movementController.setUnit(localUnit);

    // Обработчик начала атаки - сообщаем MovementController
    animationController.onAttackStart(() => {
        movementController.setAttackStarted();
        staminaPredictor.onAttackStart();
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
    await new Promise<void>((resolve) => {
        const checkInterval = setInterval(() => {
            // If we have a player ID, we're connected
            const playerId = networkManager.getPlayerId();
            if (playerId) {
                clearInterval(checkInterval);

                // Set player position from server (server sends world coordinates)
                const initialPosition = networkManager.getInitialPosition();

                // Only set the movement controller - it will handle coordinate conversion
                movementController.setInitialPosition(initialPosition.x, initialPosition.y);

                resolve();
            }
        }, 100);
    });
    console.log("Network connection established, starting game...");

    // A dropped connection reconnects as a new server-side player at a new spawn.
    // Without re-seeding the predicted position here, the first movement ACK of the
    // new session would arrive as a large correction and snap the local player.
    networkManager.onSessionStart((position) => {
        movementController.setInitialPosition(position.x, position.y);
    });

    // Local player attack animation — triggered by server confirmation via gameState
    networkManager.onPlayerAttack((playerId, _position, comboStep) => {
        if (playerId === networkManager.getPlayerId()) {
            animationController.handleAttack(comboStep);
        }
    });

        // Attack handling — no prediction, server-authoritative animation
    app.canvas.addEventListener("mousedown", (e) => {
        if (e.button === 0 && animationController.playerState !== PlayerState.ATTACKING) {
            // Gate: don't spam while animation is still playing locally
            const position = movementController.getScreenPosition();
            const attackMsg = {
                type: 'attack' as const,
                position
            };
            const binaryData = BinaryProtocol.encodeAttack(attackMsg);
            networkManager.sendAttack(binaryData);
        }
    });

    // Block handling (RMB, GDD §54) — held state, server-authoritative: it silently
    // no-ops for units with no block profile and auto-cancels on movement/empty
    // stamina (see server TryBlockStart/updateBlockDrain), so the client just
    // forwards press/release intent.
    app.canvas.addEventListener("contextmenu", (e) => e.preventDefault());
    app.canvas.addEventListener("mousedown", (e) => {
        if (e.button !== 2) return;
        // Predict block locally before the BLOCK_START round-trip: otherwise the
        // client keeps resending the held WASD vector every tick while waiting for
        // confirmation, which immediately cancels the server's freshly-entered
        // block state the instant it reasserts non-zero velocity (see world.go
        // updateBlockDrain) — block would then only ever stick by lucky timing.
        // Gated on the unit having a block profile at all, same as the server.
        if (localUnit.block) movementController.requestBlock();
        networkManager.sendBlockStart();
    });
    app.canvas.addEventListener("mouseup", (e) => {
        if (e.button !== 2) return;
        movementController.releaseBlock();
        networkManager.sendBlockEnd();
    });
    // A backgrounded tab may never deliver the mouseup — release block so it
    // doesn't stay stuck server-side (mirrors InputManager's key-state suspend).
    window.addEventListener("blur", () => {
        movementController.releaseBlock();
        networkManager.sendBlockEnd();
    });

    // Fixed timestep for physics updates
    const nominalFixedTimeStep = 1 / TICK_RATE;
    let accumulator = 0;

    // Game loop
    console.log("Starting game loop...");
    app.ticker.add((time) => {
        const deltaTime = time.deltaTime / 60; // Convert to seconds
        // Update FPS display
        fpsDisplay.update();

        // Time dilation (EVE-style TiDi): when the server slows its own tick rate
        // under pressure, it tells every client via dilationPct on each state frame.
        // Stretching the local fixed step by the same factor keeps prediction in
        // lockstep instead of running ahead of a server that is deliberately slower.
        const dilationPct = Math.max(networkManager.getDilationPct(), 1);
        const fixedTimeStep = nominalFixedTimeStep * (100 / dilationPct);

        // Do not replay seconds of stale input after a suspended/background tab.
        // Client and server both advance through explicit input steps, so dropping
        // excess wall-clock catch-up here preserves their deterministic sequence.
        accumulator = Math.min(accumulator + deltaTime, fixedTimeStep * 4);

        // Process physics at fixed time steps
        while (accumulator >= fixedTimeStep) {
            // Always update movement - let MovementController decide attack behavior
            const isMoving = movementController.update(fixedTimeStep);
            movementController.updateFacing(input.mousePosition.x, input.mousePosition.y);

            // Set animation state based on attack/block state and movement. Block is
            // server-authoritative (see BLOCK_START/BLOCK_END handlers above) — the
            // local player's own gameState record carries it like any other player's.
            const isBlocking = networkManager.getPlayers()[networkManager.getPlayerId()]?.blocking ?? false;
            if (animationController.playerState !== PlayerState.ATTACKING) {
                if (isBlocking) {
                    animationController.setState(PlayerState.BLOCKING);
                } else {
                    animationController.setState(
                        isMoving ? PlayerState.MOVING : PlayerState.IDLE,
                        movementController.isSprinting
                    );
                }
            }
            // During attack, keep ATTACKING state and don't change it

            // Predict stamina drain/regen at the same fixed cadence as the rest of
            // the simulation (see staminaPredictor.ts) — corrected by the roster
            // reconcile callback above whenever the real value arrives.
            staminaPredictor.update(fixedTimeStep, { blocking: isBlocking, sprinting: movementController.isSprinting });

            // Decrease accumulated time
            accumulator -= fixedTimeStep;
        }

        // Blend the local player's rendered position between the last two ticks —
        // physics only advances at TICK_RATE, but this callback (and thus the
        // actual draw) runs at display refresh rate, which is normally higher.
        // Without this the sprite sits still for several frames then jumps.
        movementController.render(accumulator / fixedTimeStep);

        // Interpolation is a rendering concern and must run on every rendered frame,
        // independently from the fixed-rate local simulation loop.
        playerManager.update(deltaTime);

        // Update animation and visual state (can run at variable frame rate)
        animationController.playerRef.scale.copyFrom(movementController.scale);

        localStatusBar.update(
            networkManager.getHp(),
            localUnit.hp,
            staminaPredictor.current,
            localUnit.stamina
        );
        localStatusBar.setPosition(playerSprite.position.x, playerSprite.position.y - playerSprite.height / 2);
    });
    console.log("Game loop started");
})();
