
import { Application } from "pixi.js";
import { SpriteLoader } from "./spriteLoader";
import { FpsDisplay } from "./utils/fpsDisplay";
import { InputManager } from "./utils/inputManager";
import { MovementController } from "./controllers/movementController";
import { AnimationController } from "./controllers/animationController";

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
    const character = await SpriteLoader.loadCharacter("/assets/16x16_knight_3_v3.png");

    // Setup player
    const baseScale = 2;
    const initialPlayer = character.getAnimation("idle")!;
    initialPlayer.position.set(app.screen.width / 2, app.screen.height / 2);
    initialPlayer.scale.set(baseScale);
    initialPlayer.animationSpeed = 0.1;
    app.stage.addChild(initialPlayer);

    const animationController = new AnimationController(character.animations, initialPlayer);
    const movementController = new MovementController(
        input,
        initialPlayer.position,
        initialPlayer.scale
    );

    // Attack handling
    app.canvas.addEventListener("mousedown", (e) => {
        if (e.button === 0 && animationController.handleAttack()) {
            animationController.setAnimation("attack", movementController.isMoving);
        }
    });

    // Game loop
    app.ticker.add((time) => {
        fpsDisplay.update();

        const deltaTime = time.deltaTime;
        const isMoving = movementController.update(deltaTime);
        movementController.updateScale(input.mousePosition.x);

        animationController.setAnimation(
            isMoving ? "run" : "idle",
            isMoving
        );

        // Update scale reference for mouse flipping
        animationController.playerRef.scale.copyFrom(movementController.scale);
    });
})();
// declare module 'pixi.js' {
//     interface AnimatedSprite {
//         currentAnimation?: string;
//     }
// }

// import { Application, Point, Text } from "pixi.js";

// import { SpriteLoader } from "./spriteLoader";

// // Store keyboard state
// const keysPressed: { [key: string]: boolean } = {};
// // Store mouse position
// const mousePosition: Point = new Point(0, 0);

// (async () => {
//     const app = new Application();
//     await app.init({
//         background: "#1099bb",
//         resizeTo: window,
//         eventMode: "static", // Enable interactivity
//         antialias: true
//     });

//     const fpsText = new Text({
//         text: "FPS: 0",
//         style: {
//             fontFamily: "Arial",
//             fontSize: 16,
//             fill: 0xffffff,
//             align: "left"
//         }
//     });
//     fpsText.position.set(10, 10);
//     app.stage.addChild(fpsText);

//     // Update FPS counter in game loop
//     app.ticker.add(() => {
//         fpsText.text = `FPS: ${Math.round(app.ticker.FPS)}`;
//     });


//     document.getElementById("pixi-container")!.appendChild(app.canvas);

//     // Load character animations
//     const character = await SpriteLoader.loadCharacter("/assets/16x16_knight_3_v3.png") as Awaited<ReturnType<typeof SpriteLoader.loadCharacter>>;

//     // Setup player
//     let player = character.getAnimation("idle")!;
//     const baseScale = 2; // Define your scale factor here
//     player.position.set(app.screen.width / 2, app.screen.height / 2);
//     player.scale.set(baseScale); // Scale to match your needs
//     player.animationSpeed = 0.1; // Adjust animation speed
//     player.play();
//     app.stage.addChild(player);

//     // Keyboard input handling
//     window.addEventListener("keydown", (e) => {
//         keysPressed[e.key.toLowerCase()] = true;
//     });

//     window.addEventListener("keyup", (e) => {
//         keysPressed[e.key.toLowerCase()] = false;
//     });

//     let isAttacking = false;
//     let attackCooldown = false;

//     app.canvas.addEventListener("mousedown", (e) => {
//         if (e.button === 0 && !attackCooldown) { // Left mouse button
//             isAttacking = true;
//             attackCooldown = true;
//             setTimeout(() => attackCooldown = false, 500); // 0.5s cooldown
//         }
//     });
//     // Mouse position tracking
//     app.canvas.addEventListener("mousemove", (e) => {
//         const rect = app.canvas.getBoundingClientRect();
//         mousePosition.x = e.clientX - rect.left;
//         mousePosition.y = e.clientY - rect.top;
//     });

//     function setAnimation(name: string) {
//         if (player.playing && player.currentAnimation === name) return;

//         const newAnim = character.getAnimation(name);
//         if (!newAnim) return;

//         // Handle attack animation completion
//         if (name === "attack") {
//             newAnim.loop = false;
//             newAnim.onComplete = () => {
//                 attackCooldown = false;
//                 // setAnimation(isMoving ? "run" : "idle");
//             };
//         } else {
//             newAnim.loop = true;
//         }

//         // Copy current state
//         newAnim.x = player.x;
//         newAnim.y = player.y;
//         newAnim.scale.copyFrom(player.scale);
//         newAnim.animationSpeed = player.animationSpeed;

//         // Swap sprites
//         app.stage.removeChild(player);
//         player = newAnim;
//         app.stage.addChild(player);
//         player.play();

//         // Update current animation name
//         player.currentAnimation = name;
//     }
//     // Game loop
//     app.ticker.add((time) => {
//         const speed = 5 * time.deltaTime;
//         let isMoving = false;

//         // Handle attack animation
//         if (isAttacking) {
//             setAnimation("attack");
//             isAttacking = false; // Reset attack trigger
//         }

//         // Handle movement
//         if (keysPressed["w"]) { player.y -= speed; isMoving = true; }
//         if (keysPressed["s"]) { player.y += speed; isMoving = true; }
//         if (keysPressed["a"]) { player.x -= speed; isMoving = true; }
//         if (keysPressed["d"]) { player.x += speed; isMoving = true; }

//         // Flip sprite based on mouse position
//         if (mousePosition.x < player.x) {
//             player.scale.set(-baseScale, baseScale);
//         } else {
//             player.scale.set(baseScale, baseScale);
//         }

//         // Update animation state
//         if (isMoving) {
//             setAnimation("run");
//         } else {
//             setAnimation("idle");
//         }
//     });
// })();
