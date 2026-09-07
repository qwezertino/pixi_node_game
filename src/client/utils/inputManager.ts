type KeysPressed = { [key: string]: boolean };

export class InputManager {
    keysPressed: KeysPressed = {};
    mousePosition = { x: 0, y: 0 };
    private f3Pressed: boolean = false;
    private onF3Callback: (() => void) | null = null;
    private onSuspendCallback: (() => void) | null = null;
    private onResumeCallback: (() => void) | null = null;
    private suspended = document.visibilityState === "hidden";

    constructor(private canvas: HTMLCanvasElement) {
        this.init();
    }

    private init() {

        window.addEventListener("keydown", (e) => {
            this.keysPressed[e.code] = true;
            if (e.code === "ShiftLeft" || e.code === "ShiftRight") {
                this.keysPressed["Shift"] = true;
            }

            if (e.code === "F3" && !this.f3Pressed) {
                this.f3Pressed = true;
                e.preventDefault();
                if (this.onF3Callback) {
                    this.onF3Callback();
                }
            }
        });

        window.addEventListener("keyup", (e) => {
            this.keysPressed[e.code] = false;
            if (e.code === "ShiftLeft" || e.code === "ShiftRight") {
                this.keysPressed["Shift"] = !!this.keysPressed["ShiftLeft"] || !!this.keysPressed["ShiftRight"];
            }

            if (e.code === "F3") {
                this.f3Pressed = false;
            }
        });

        window.addEventListener("blur", () => this.suspend());
        window.addEventListener("focus", () => {
            if (document.visibilityState === "visible") this.resume();
        });
        document.addEventListener("visibilitychange", () => {
            if (document.visibilityState === "hidden") {
                this.suspend();
            } else {
                this.resume();
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

    setLifecycleCallbacks(onSuspend: () => void, onResume: () => void) {
        this.onSuspendCallback = onSuspend;
        this.onResumeCallback = onResume;
    }

    private suspend() {
        if (this.suspended) return;
        this.suspended = true;
        this.keysPressed = {};
        this.f3Pressed = false;
        this.onSuspendCallback?.();
    }

    private resume() {
        if (!this.suspended) return;
        this.suspended = false;
        this.onResumeCallback?.();
    }
}
