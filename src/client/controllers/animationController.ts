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

    public setState(state: PlayerState, sprinting = false, elapsedMs = 0) {
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
                this.startAttackAnimation(elapsedMs);
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
    private startAttackAnimation(elapsedMs = 0) {
        this.attackAnimationPlaying = true;
        const variant = `attack${this.attackStep}`;
        const base = this.characterVisual.directional
            ? (this.characterVisual.animations.has(this.resolveKey(variant)) ? variant : "attack1")
            : "attack";
        this.setAnimation(base, true);

        this.playerSprite.loop = false;
        this.playerSprite.onComplete = () => {
            this.attackAnimationPlaying = false;
            this.setState(PlayerState.IDLE);

            if (this.onAttackEndCallback) {
                this.onAttackEndCallback();
            }
        };

        if (elapsedMs > 0) {
            const totalFrames = this.playerSprite.totalFrames;
            const fps = 60 * (this.playerSprite.animationSpeed || 1);
            const totalDurationMs = (totalFrames / fps) * 1000;

            if (elapsedMs >= totalDurationMs) {
                this.playerSprite.onComplete?.();
                return;
            }

            const frame = Math.min(totalFrames - 1, Math.floor((elapsedMs / totalDurationMs) * totalFrames));
            this.playerSprite.gotoAndPlay(frame);
        }
    }

    public onAttackEnd(callback: () => void) {
        this.onAttackEndCallback = callback;
    }

    public onAttackStart(callback: () => void) {
        this.onAttackStartCallback = callback;
    }

    handleAttack(step: number, elapsedMs = 0) {
        this.attackStep = step;

        if (this.onAttackStartCallback) {
            this.onAttackStartCallback();
        }

        this.setState(PlayerState.ATTACKING, false, elapsedMs);
        return true;
    }
}
