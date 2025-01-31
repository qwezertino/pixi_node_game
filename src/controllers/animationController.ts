import { AnimatedSprite } from "pixi.js";

declare module "pixi.js" {
    interface AnimatedSprite {
        currentAnimation?: string;
    }
}

export class AnimationController {
    private currentAnimation: string = "idle";
    private attackCooldown = false;

    constructor(
        private animations: Map<string, AnimatedSprite>,
        private player: AnimatedSprite
    ) {}

    setAnimation(name: string, isMoving: boolean) {
        if (this.player.currentAnimation === name) return;

        const textures = this.animations.get(name);
        if (!textures) return;

        const newAnim = new AnimatedSprite(textures);
        this.transferState(newAnim);
        this.handleAttackCompletion(newAnim, isMoving);

        this.player.destroy();
        this.player = newAnim;
        this.currentAnimation = name;
    }

    private transferState(newAnim: AnimatedSprite) {
        newAnim.position.copyFrom(this.player.position);
        newAnim.scale.copyFrom(this.player.scale);
        newAnim.animationSpeed = this.player.animationSpeed;
        newAnim.anchor.set(0.5);
        newAnim.play();
    }

    private handleAttackCompletion(newAnim: AnimatedSprite, isMoving: boolean) {
        if (newAnim.currentAnimation === "attack") {
            newAnim.loop = false;
            newAnim.onComplete = () => {
                this.attackCooldown = false;
                this.setAnimation(isMoving ? "run" : "idle", isMoving);
            };
        } else {
            newAnim.loop = true;
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
        return this.player;
    }
}