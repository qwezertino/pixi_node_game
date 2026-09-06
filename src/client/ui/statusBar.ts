import { Container, Graphics } from "pixi.js";

// Small HP/stamina bar pair rendered above a unit's head. Values are clamped to
// [0, max] defensively — until real damage/drain exists these always report full,
// but the UI itself must stay correct once combat starts writing partial values.
const BAR_WIDTH = 48;
const BAR_HEIGHT = 6;
const BAR_GAP = 2;
const STACK_HEIGHT = BAR_HEIGHT * 2 + BAR_GAP;
// Gap between the sprite's visual top and the bottom of the stamina bar.
const VERTICAL_OFFSET = 10;

const HP_COLOR = 0x3ecb3e;
const STAMINA_COLOR = 0x3ea0f0;
const BG_COLOR = 0x1a1a1a;

export class StatusBarWidget {
    readonly container: Container;
    private hpFill = new Graphics();
    private staminaFill = new Graphics();
    private lastHpRatio = -1;
    private lastStaminaRatio = -1;

    constructor() {
        this.container = new Container();

        const hpBg = new Graphics().rect(0, 0, BAR_WIDTH, BAR_HEIGHT).fill(BG_COLOR);
        const staminaBg = new Graphics()
            .rect(0, BAR_HEIGHT + BAR_GAP, BAR_WIDTH, BAR_HEIGHT)
            .fill(BG_COLOR);

        this.container.addChild(hpBg, staminaBg, this.hpFill, this.staminaFill);
        // Anchor at bottom-center so the stack sits just above the sprite's head
        // regardless of which bars are currently drawn.
        this.container.pivot.set(BAR_WIDTH / 2, STACK_HEIGHT);
    }

    update(hp: number | undefined, maxHp: number, stamina: number | undefined, maxStamina: number) {
        this.setBar(this.hpFill, true, hp, maxHp);
        this.setBar(this.staminaFill, false, stamina, maxStamina);
    }

    private setBar(fill: Graphics, isHp: boolean, value: number | undefined, max: number) {
        const ratio = max > 0 ? Math.max(0, Math.min(1, (value ?? max) / max)) : 0;
        if ((isHp ? this.lastHpRatio : this.lastStaminaRatio) === ratio) return;
        if (isHp) this.lastHpRatio = ratio;
        else this.lastStaminaRatio = ratio;

        fill.clear();
        if (ratio <= 0) return;
        const y = isHp ? 0 : BAR_HEIGHT + BAR_GAP;
        fill.rect(0, y, BAR_WIDTH * ratio, BAR_HEIGHT).fill(isHp ? HP_COLOR : STAMINA_COLOR);
    }

    /** `headX/headY` is the sprite's top-center point in the same coordinate space. */
    setPosition(headX: number, headY: number) {
        this.container.position.set(headX, headY - VERTICAL_OFFSET);
    }

    destroy() {
        this.container.destroy({ children: true });
    }
}
