import { AnimatedSprite, Texture } from "pixi.js";

declare module "pixi.js" {
    interface AnimatedSprite {
        currentAnimation?: string;
    }
}

export class AnimationController {
    private currentAnimation: string = "idle";
    private attackCooldown = false;
    private playerSprite: AnimatedSprite;

    constructor(
        private animations: Map<string, Texture[]>,
        initialPlayer: AnimatedSprite
    ) {
        this.playerSprite = initialPlayer;
        this.playerSprite.currentAnimation = this.currentAnimation;
    }

    setAnimation(name: string, isMoving: boolean) {
        if (this.playerSprite.playing && this.playerSprite.currentAnimation === name) return;

        const textures = this.animations.get(name);
        if (!textures) return;

        console.log("animation to set:" + name + " | player playing: ", this.playerSprite.playing + " | current animation: " + this.playerSprite.currentAnimation);
        this.handleAttackCompletion(this.playerSprite, isMoving);

        this.playerSprite.textures = textures;

        this.playerSprite.play();
        this.currentAnimation = name;
        this.playerSprite.currentAnimation = name;
    }
    private handleAttackCompletion(playerSprite: AnimatedSprite, isMoving: boolean) {
        if (playerSprite.currentAnimation === "attack") {
            playerSprite.loop = false;
            console.log("handle attack: " + playerSprite.currentAnimation, );
            playerSprite.onComplete = () => {
                this.attackCooldown = false;
                this.setAnimation(isMoving ? "run" : "idle", isMoving);
            };
        } else {
            playerSprite.loop = true;
        }
    }

    handleAttack() {
        if (!this.attackCooldown) {
            this.attackCooldown = true;
            setTimeout(() => this.attackCooldown = false, 500);
            return true;
        }
        return false;
    }

    get playerRef() {
        return this.playerSprite;
    }
}