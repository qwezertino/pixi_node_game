import { AnimatedSprite, Texture } from "pixi.js";

export enum PlayerState {
    IDLE = "idle",
    MOVING = "moving",
    ATTACKING = "attacking",
}

declare module "pixi.js" {
    interface AnimatedSprite {
        currentAnimation?: string;
    }
}

export class AnimationController {
    private currentAnimation: string = "idle";
    private currentState: PlayerState = PlayerState.IDLE;
    private attackAnimationPlaying = false;

    private playerSprite: AnimatedSprite;

    get playerRef() {
        return this.playerSprite;
    }
    get playerState() {
        return this.currentState;
    }

    constructor(
        private animations: Map<string, Texture[]>,
        initialPlayer: AnimatedSprite
    ) {
        this.playerSprite = initialPlayer;
        this.playerSprite.currentAnimation = this.currentAnimation;
    }

    public setState(state: PlayerState) {
        if (this.attackAnimationPlaying && state !== PlayerState.ATTACKING) {
            // Block state changes while attack animation is playing
            return;
        }

        this.currentState = state;

        switch (state) {
            case PlayerState.IDLE:
                this.setAnimation("idle");
                break;
            case PlayerState.MOVING:
                this.setAnimation("run");
                break;
            case PlayerState.ATTACKING:
                this.startAttackAnimation();
                break;
        }
    }

    setAnimation(name: string) {
        if (this.playerSprite.playing && this.playerSprite.currentAnimation === name) return;
        const textures = this.animations.get(name);
        if (!textures) return;

        this.playerSprite.textures = textures;

        this.playerSprite.play();
        this.currentAnimation = name;
        this.playerSprite.currentAnimation = name;
    }
    private startAttackAnimation() {
        this.attackAnimationPlaying = true;
        this.setAnimation("attack");

        // Play attack animation and block other animations and movement
        this.playerSprite.loop = false;
        this.playerSprite.onComplete = () => {
            this.attackAnimationPlaying = false;
            this.setState(PlayerState.IDLE); // or PlayerState.MOVING if you want to continue moving after attack
        };
    }
    handleAttack() {
        this.setState(PlayerState.ATTACKING);
    }
}