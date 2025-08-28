type KeysPressed = { [key: string]: boolean };

export class InputManager {
    keysPressed: KeysPressed = {};
    mousePosition = { x: 0, y: 0 };
    private f3Pressed: boolean = false;
    private onF3Callback: (() => void) | null = null;

    constructor(private canvas: HTMLCanvasElement) {
        this.init();
    }

    private init() {
        window.addEventListener("keydown", (e) => {
            this.keysPressed[e.key.toLowerCase()] = true;

            // Handle F3 key specifically
            if (e.key === 'F3' && !this.f3Pressed) {
                this.f3Pressed = true;
                e.preventDefault(); // Prevent browser's default F3 behavior
                if (this.onF3Callback) {
                    this.onF3Callback();
                }
            }
        });

        window.addEventListener("keyup", (e) => {
            this.keysPressed[e.key.toLowerCase()] = false;

            // Reset F3 pressed state
            if (e.key === 'F3') {
                this.f3Pressed = false;
            }
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

    setF3Callback(callback: () => void) {
        this.onF3Callback = callback;
    }
}