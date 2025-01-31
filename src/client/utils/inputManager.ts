type KeysPressed = { [key: string]: boolean };

export class InputManager {
    keysPressed: KeysPressed = {};
    mousePosition = { x: 0, y: 0 };

    constructor(private canvas: HTMLCanvasElement) {
        this.init();
    }

    private init() {
        window.addEventListener("keydown", (e) => {
            this.keysPressed[e.key.toLowerCase()] = true;
        });

        window.addEventListener("keyup", (e) => {
            this.keysPressed[e.key.toLowerCase()] = false;
        });

        this.canvas.addEventListener("mousemove", (e) => {
            const rect = this.canvas.getBoundingClientRect();
            this.mousePosition.x = e.clientX - rect.left;
            this.mousePosition.y = e.clientY - rect.top;
        });
    }

    isKeyDown(key: string) {
        return !!this.keysPressed[key];
    }
}