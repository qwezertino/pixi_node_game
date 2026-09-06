import { AnimatedSprite } from "pixi.js";
import type { CharacterVisual } from "../utils/spriteLoader";
import type { Direction } from "../utils/animationLayout";

export enum PlayerState {
    IDLE = "idle",
    MOVING = "moving",
    ATTACKING = "attacking",
    BLOCKING = "blocking",
}

declare module "pixi.js" {
    interface AnimatedSprite {
        currentAnimation?: string;
    }
}

export class AnimationController {
    private currentState: PlayerState = PlayerState.IDLE;
    private currentBase: string = "idle";
    private direction: Direction = "right";
    private attackAnimationPlaying = false;
    // Which combo step to play next (1-indexed) — set by handleAttack right
    // before setState(ATTACKING), consumed by startAttackAnimation. Server-
    // authoritative (see PlayerState.comboStep); the client no longer picks
    // attack1/attack2 randomly.
    private attackStep = 1;
    private onAttackEndCallback: (() => void) | null = null;
    private onAttackStartCallback: (() => void) | null = null;

    private playerSprite: AnimatedSprite;

    get playerRef() {
        return this.playerSprite;
    }
    get playerState() {
        return this.currentState;
    }

    constructor(
        private characterVisual: CharacterVisual,
        initialPlayer: AnimatedSprite
    ) {
        this.playerSprite = initialPlayer;
        this.playerSprite.currentAnimation = this.resolveKey(this.currentBase);
    }

    public setState(state: PlayerState, sprinting = false) {
        if (this.attackAnimationPlaying && state !== PlayerState.ATTACKING) {
            return;
        }

        this.currentState = state;

        switch (state) {
            case PlayerState.IDLE:
                this.setAnimation("idle");
                break;
            case PlayerState.MOVING: {
                const base = sprinting ? "run" : "walk";
                this.setAnimation(this.characterVisual.animations.has(this.resolveKey(base)) ? base : "run");
                break;
            }
            case PlayerState.ATTACKING:
                this.startAttackAnimation();
                break;
            case PlayerState.BLOCKING:
                this.setAnimation("ready");
                break;
        }
    }

    public setDirection(direction: Direction) {
        if (this.attackAnimationPlaying) return;
        if (this.direction === direction) return;
        this.direction = direction;

        if (this.characterVisual.directional) {
            this.setAnimation(this.currentBase, true);
        } else {
            const sign = direction === "left" ? -1 : 1;
            this.playerSprite.scale.x = sign * Math.abs(this.playerSprite.scale.x);
        }
    }

    private resolveKey(base: string): string {
        return this.characterVisual.directional ? `${base}_${this.direction}` : base;
    }

    setAnimation(base: string, force = false) {
        this.currentBase = base;
        const key = this.resolveKey(base);
        if (!force && this.playerSprite.playing && this.playerSprite.currentAnimation === key) return;

        const textures = this.characterVisual.animations.get(key);
        if (!textures) return;

        this.playerSprite.textures = textures;
        this.playerSprite.loop = true;
        this.playerSprite.onComplete = undefined;

        this.playerSprite.play();
        this.playerSprite.currentAnimation = key;
    }
    private startAttackAnimation() {
        this.attackAnimationPlaying = true;
        const variant = `attack${this.attackStep}`;
        const base = this.characterVisual.directional
            ? (this.characterVisual.animations.has(this.resolveKey(variant)) ? variant : "attack1")
            : "attack";
        // force=true: a combo continuation can arrive while attack1 is still
        // playing (same PlayerState.ATTACKING throughout, see
        // networkManager.ts's comboAdvanced check) — it must restart the sprite
        // on the new step's frames rather than being swallowed by setAnimation's
        // "already playing this key" guard.
        this.setAnimation(base, true);

        this.playerSprite.loop = false;
        this.playerSprite.onComplete = () => {
            this.attackAnimationPlaying = false;
            this.setState(PlayerState.IDLE);

            if (this.onAttackEndCallback) {
                this.onAttackEndCallback();
            }
        };
    }

    public onAttackEnd(callback: () => void) {
        this.onAttackEndCallback = callback;
    }

    public onAttackStart(callback: () => void) {
        this.onAttackStartCallback = callback;
    }

    // step: 1-indexed combo step (see PlayerState.comboStep) — the server is
    // authoritative on this, so every call here is a genuinely new swing worth
    // playing, even if the previous one (same PlayerState.ATTACKING throughout)
    // hasn't finished its own animation yet (see startAttackAnimation's force).
    handleAttack(step: number) {
        this.attackStep = step;

        // Вызовем callback начала атаки
        if (this.onAttackStartCallback) {
            this.onAttackStartCallback();
        }

        this.setState(PlayerState.ATTACKING);
        return true;
    }
}
