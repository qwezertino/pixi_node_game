import { Application, Assets, Sprite, Point } from "pixi.js";

// Store keyboard state
const keysPressed: { [key: string]: boolean } = {};
// Store mouse position
const mousePosition: Point = new Point(0, 0);

(async () => {
    const app = new Application();
    await app.init({
        background: "#1099bb",
        resizeTo: window,
        eventMode: "static", // Enable interactivity
        antialias: true
    });

    document.getElementById("pixi-container")!.appendChild(app.canvas);

    // Load player texture
    const texture = await Assets.load("/assets/Humans/Human1.png");
    const player = new Sprite(texture);

    // Set up player properties
    player.anchor.set(0.5);
    player.position.set(app.screen.width / 2, app.screen.height / 2);
    const baseScale = 2; // Define your scale factor here
    player.scale.set(baseScale); // Initial uniform scaling

    app.stage.addChild(player);

    // Keyboard input handling
    window.addEventListener("keydown", (e) => {
        keysPressed[e.key.toLowerCase()] = true;
    });

    window.addEventListener("keyup", (e) => {
        keysPressed[e.key.toLowerCase()] = false;
    });

    // Mouse position tracking
    app.canvas.addEventListener("mousemove", (e) => {
        const rect = app.canvas.getBoundingClientRect();
        mousePosition.x = e.clientX - rect.left;
        mousePosition.y = e.clientY - rect.top;
    });

    // Game loop
    app.ticker.add((time) => {
        const speed = 5 * time.deltaTime;

        // Movement handling
        if (keysPressed["w"]) player.y -= speed;
        if (keysPressed["s"]) player.y += speed;
        if (keysPressed["a"]) player.x -= speed;
        if (keysPressed["d"]) player.x += speed;

        // Flip sprite based on mouse position
        if (mousePosition.x < player.x) {
          player.scale.set(-baseScale, baseScale); // Flip X, keep Y scale
        } else {
            player.scale.set(baseScale, baseScale); // Maintain original scale
        }
    });
})();
