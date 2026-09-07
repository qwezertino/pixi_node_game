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

    const canvas = document.createElement('canvas');
    const gl = canvas.getContext('webgl') || canvas.getContext('experimental-webgl');

    let useCanvas = false;
    if (!gl || !(gl instanceof WebGLRenderingContext)) {
        console.warn('WebGL not supported, falling back to Canvas renderer');
        useCanvas = true;
    } else {

        const debugInfo = gl.getExtension('WEBGL_debug_renderer_info');
        if (debugInfo) {
            const renderer = gl.getParameter(debugInfo.UNMASKED_RENDERER_WEBGL);
            console.log('WebGL Renderer:', renderer);

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

    const worldBackground = new Graphics();
    worldBackground.rect(0, 0, app.screen.width, app.screen.height);
    worldBackground.fill(parseInt(COLORS.worldBackground.replace('#', ''), 16));
    app.stage.addChild(worldBackground);

    const playerContainer = new Container();
    playerContainer.eventMode = "none";
    playerContainer.interactiveChildren = false;
    app.stage.addChild(playerContainer);

    const input = new InputManager(app.canvas);

    const networkManager = new NetworkManager();

    const fpsDisplay = new FpsDisplay(app, networkManager);

    input.setF3Callback(() => {
        fpsDisplay.toggleDetailedStats();
    });

    mountUnitViewerToggle();

    const coordinateConverter = new CoordinateConverter(app.screen.width, app.screen.height);

    const playerManager = new PlayerManager(playerContainer, networkManager, coordinateConverter);

    const localUnit = await showUnitSelectScreen(app);
    networkManager.connect(localUnit.id);

    const characterVisual = await SpriteLoader.loadUnitCharacterVisual(localUnit).catch(() =>
        SpriteLoader.loadCharacterVisual("/assets/16x16_knight_3_v3.png")
    );

    const playerSprite = characterVisual.getAnimation(characterVisual.directional ? "idle_right" : "idle")!;
    playerSprite.scale.set(characterVisual.scale);
    playerSprite.animationSpeed = localUnit.animationSpeed;
    playerSprite.play();

    const virtualCenter = coordinateConverter.getVirtualCenter();
    const screenCenter = coordinateConverter.virtualToScreen(virtualCenter.x, virtualCenter.y);
    playerSprite.position.set(screenCenter.x, screenCenter.y);
    playerContainer.addChild(playerSprite);

    const localStatusBar = new StatusBarWidget();
    playerContainer.addChild(localStatusBar.container);

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

    animationController.onAttackStart(() => {
        movementController.setAttackStarted();
        staminaPredictor.onAttackStart();
    });

    animationController.onAttackEnd(() => {
        movementController.onAttackEnd();
        networkManager.sendAttackEnd();
    });

    movementController.setNetworkManager(networkManager);
    movementController.setAnimationController(animationController);
    movementController.setCoordinateConverter(coordinateConverter);

    playerManager.setMovementController(movementController);

    networkManager.onMovementAck((position, inputSequence) => {
        movementController.handleMovementAcknowledgment(position, inputSequence);
    });

    const handleResize = () => {
        const newWidth = app.screen.width;
        const newHeight = app.screen.height;

        coordinateConverter.updateScreenSize(newWidth, newHeight);

        worldBackground.clear();
        worldBackground.rect(0, 0, newWidth, newHeight);
        worldBackground.fill(parseInt(COLORS.worldBackground.replace('#', ''), 16));

        playerManager.updateAllPlayerPositions();
    };

    window.addEventListener('resize', handleResize);

    await new Promise<void>((resolve) => {
        const checkInterval = setInterval(() => {

            const playerId = networkManager.getPlayerId();
            if (playerId) {
                clearInterval(checkInterval);

                const initialPosition = networkManager.getInitialPosition();

                movementController.setInitialPosition(initialPosition.x, initialPosition.y);

                resolve();
            }
        }, 100);
    });
    console.log("Network connection established, starting game...");

    networkManager.onSessionStart((position) => {
        movementController.setInitialPosition(position.x, position.y);
    });

    networkManager.onPlayerAttack((playerId, _position, comboStep) => {
        if (playerId === networkManager.getPlayerId()) {
            animationController.handleAttack(comboStep);
        }
    });

    app.canvas.addEventListener("mousedown", (e) => {
        if (e.button === 0 && animationController.playerState !== PlayerState.ATTACKING) {

            const position = movementController.getScreenPosition();
            const attackMsg = {
                type: 'attack' as const,
                position
            };
            const binaryData = BinaryProtocol.encodeAttack(attackMsg);
            networkManager.sendAttack(binaryData);
        }
    });

    app.canvas.addEventListener("contextmenu", (e) => e.preventDefault());
    app.canvas.addEventListener("mousedown", (e) => {
        if (e.button !== 2) return;

        if (localUnit.block) movementController.requestBlock();
        networkManager.sendBlockStart();
    });
    app.canvas.addEventListener("mouseup", (e) => {
        if (e.button !== 2) return;
        movementController.releaseBlock();
        networkManager.sendBlockEnd();
    });

    window.addEventListener("blur", () => {
        movementController.releaseBlock();
        networkManager.sendBlockEnd();
    });

    const nominalFixedTimeStep = 1 / TICK_RATE;
    let accumulator = 0;

    console.log("Starting game loop...");
    app.ticker.add((time) => {
        const deltaTime = time.deltaTime / 60;

        fpsDisplay.update();

        const dilationPct = Math.max(networkManager.getDilationPct(), 1);
        const fixedTimeStep = nominalFixedTimeStep * (100 / dilationPct);

        accumulator = Math.min(accumulator + deltaTime, fixedTimeStep * 4);

        while (accumulator >= fixedTimeStep) {

            const isMoving = movementController.update(fixedTimeStep);
            movementController.updateFacing(input.mousePosition.x, input.mousePosition.y);

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

            staminaPredictor.update(fixedTimeStep, { blocking: isBlocking, sprinting: movementController.isSprinting });

            accumulator -= fixedTimeStep;
        }

        movementController.render(accumulator / fixedTimeStep);

        playerManager.update(deltaTime);

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
