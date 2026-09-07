import { Application, Container, Graphics, type AnimatedSprite } from "pixi.js";
import { SpriteLoader } from "./utils/spriteLoader";
import { FpsDisplay } from "./utils/fpsDisplay";
import { InputManager } from "./utils/inputManager";
import { MovementController } from "./controllers/movementController";
import { AnimationController, PlayerState } from "./controllers/animationController";
import { NetworkManager } from "./network/networkManager";
import { PlayerManager } from "./game/playerManager";
import { TICK_RATE } from "./network/protocol/messages";
import { COLORS } from "../shared/gameConfig";
import { hideLoadingScreen } from "../shared/loadingScreen";
import { BinaryProtocol } from "./network/protocol/binaryProtocol";
import { CoordinateConverter } from "./utils/coordinateConverter";
import { mountUnitViewerToggle } from "./debug/unitViewerPanel";
import { mountRespawnButton } from "./debug/respawnButton";
import { showUnitSelectScreen } from "./ui/unitSelectScreen";
import { StatusBarWidget } from "./ui/statusBar";
import { StaminaPredictor } from "./utils/staminaPredictor";
import type { UnitDefinition } from "../shared/units";

// Everything tied to one spawned life. Rebuilt by startSession() on first
// load and again on every dev "🔄 Respawn" — see the note there for why a
// page reload isn't needed. Long-lived, one-time app state (the PixiJS
// Application, NetworkManager, PlayerManager, InputManager, ...) lives
// outside this and is never recreated.
interface Session {
    localUnit: UnitDefinition;
    playerSprite: AnimatedSprite;
    localStatusBar: StatusBarWidget;
    staminaPredictor: StaminaPredictor;
    animationController: AnimationController;
    movementController: MovementController;
}

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
    hideLoadingScreen();

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

    const coordinateConverter = new CoordinateConverter(app.screen.width, app.screen.height);

    const playerManager = new PlayerManager(playerContainer, networkManager, coordinateConverter);

    // The current life. null only while startSession() is mid-flight
    // (unit-select screen showing, or waiting on the server's welcome) —
    // every listener/ticker callback below checks for that and no-ops.
    let session: Session | null = null;

    async function startSession(): Promise<void> {
        const localUnit = await showUnitSelectScreen(app);

        if (session) {
            playerContainer.removeChild(session.playerSprite);
            session.playerSprite.destroy();
            playerContainer.removeChild(session.localStatusBar.container);
        }
        session = null;

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

        session = {
            localUnit,
            playerSprite,
            localStatusBar,
            staminaPredictor,
            animationController,
            movementController,
        };
    }

    // Registered once — NetworkManager's onXxx callbacks accumulate rather
    // than replace, so re-registering per session on every respawn would
    // pile up duplicates. Each one reads whatever `session` currently is.
    networkManager.onUnitRoster((entries) => {
        if (!session) return;
        const own = entries[networkManager.getPlayerId()];
        if (own) session.staminaPredictor.reconcile(own.stamina);
    });

    networkManager.onMovementAck((position, inputSequence) => {
        session?.movementController.handleMovementAcknowledgment(position, inputSequence);
    });

    networkManager.onSessionStart((position) => {
        session?.movementController.setInitialPosition(position.x, position.y);
    });

    networkManager.onPlayerAttack((playerId, _position, comboStep) => {
        if (session && playerId === networkManager.getPlayerId()) {
            session.animationController.handleAttack(comboStep);
        }
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

    app.canvas.addEventListener("mousedown", (e) => {
        if (!session) return;
        if (e.button === 0 && session.animationController.playerState !== PlayerState.ATTACKING) {

            const position = session.movementController.getScreenPosition();
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
        if (!session || e.button !== 2) return;

        if (session.localUnit.block) session.movementController.requestBlock();
        networkManager.sendBlockStart();
    });
    app.canvas.addEventListener("mouseup", (e) => {
        if (!session || e.button !== 2) return;
        session.movementController.releaseBlock();
        networkManager.sendBlockEnd();
    });

    window.addEventListener("blur", () => {
        session?.movementController.releaseBlock();
        networkManager.sendBlockEnd();
    });

    const nominalFixedTimeStep = 1 / TICK_RATE;
    let accumulator = 0;

    console.log("Starting game loop...");
    app.ticker.add((time) => {
        fpsDisplay.update();

        if (!session) return;
        const { movementController, animationController, staminaPredictor, localUnit, playerSprite, localStatusBar } = session;

        const deltaTime = time.deltaTime / 60;

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

    if (import.meta.env.DEV) {
        mountUnitViewerToggle();
        mountRespawnButton(startSession);
    }

    await startSession();
})();
