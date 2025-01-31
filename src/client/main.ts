
// Establish WebSocket connection


// export class GameClient {
//     ws: WebSocket;

//     constructor() {
//         const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
//         const host = window.location.host;
//         const wsUrl = `${protocol}//${host}/ws`;

//         this.ws = new WebSocket(wsUrl);
//     }
// }

// document.addEventListener("DOMContentLoaded", () => {
//     new GameClient();
// });

const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
const host = 'localhost:8108'//window.location.host;
const wsUrl = `${protocol}//${host}/ws`;

const socket = new WebSocket(wsUrl);

socket.addEventListener("open", () => {
    console.log("Connected to WebSocket server");
    socket.send(JSON.stringify({ type: "hello", message: "Hello from client!" }));
});

socket.addEventListener("message", (event) => {
    console.log("Message from server:", event.data);
});

socket.addEventListener("close", () => {
    console.log("WebSocket connection closed");
});

socket.addEventListener("error", (error) => {
    console.error("WebSocket error:", error);
});

import { Application } from "pixi.js";
import { SpriteLoader } from "./utils/spriteLoader";
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
            console.log("Attacking 123");
            animationController.setAnimation("attack");
            // Send WebSocket message when player attacks
            let plPositionXY = { x: playerSprite.position.x, y: playerSprite.position.y };
            socket.send(JSON.stringify({ type: "attack", position: plPositionXY }));
        }
    });

    // Game loop
    app.ticker.add((time) => {
        fpsDisplay.update();

        const deltaTime = time.deltaTime;
        if (animationController.playerState === PlayerState.ATTACKING) {
            console.log("Attacking");
            return;
        }
        const isMoving = movementController.update(deltaTime);
        movementController.updateScale(input.mousePosition.x);
        animationController.setState(isMoving ? PlayerState.MOVING : PlayerState.IDLE);

        // Update scale reference for mouse flipping
        animationController.playerRef.scale.copyFrom(movementController.scale);
    });
})();
