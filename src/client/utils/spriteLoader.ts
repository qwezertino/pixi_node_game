import { Assets, AnimatedSprite, Texture, Rectangle } from "pixi.js";

const FRAME_SIZE = 64;
const ANIMATIONS_CONFIG = [
    { name: "idle", row: 0, frames: 5 },
    { name: "run", row: 1, frames: 8 },
    { name: "jump", row: 2, frames: 3 },
    { name: "fall", row: 3, frames: 2 },
    { name: "attack", row: 4, frames: 6 },
    { name: "hurt", row: 5, frames: 1 },
    { name: "dead", row: 6, frames: 7 },
    { name: "block", row: 7, frames: 2 }
];

export class SpriteLoader {
    static async loadCharacterVisual(spritesheetPath: string) {
        const sheetTexture = await Assets.load(spritesheetPath);
        const animations = new Map<string, Texture[]>();

        // Store texture arrays instead of AnimatedSprite instances
        for (const config of ANIMATIONS_CONFIG) {
            animations.set(config.name, this.createFrames(sheetTexture, config.row, config.frames));
        }

        return {
            animations,
            getAnimation: (name: string) => {
                const textures = animations.get(name);
                if (!textures) return undefined;

                const anim = new AnimatedSprite(textures);
                anim.anchor.set(0.5);
                return anim;
            }
        };
    }

    private static createFrames(texture: Texture, row: number, frameCount: number) {
        const frames: Texture[] = [];
        for (let i = 0; i < frameCount; i++) {
            const frame = new Rectangle(
                i * FRAME_SIZE,
                row * FRAME_SIZE,
                FRAME_SIZE,
                FRAME_SIZE
            );
            frames.push(new Texture({ source: texture.source, frame }));
        }
        return frames;
    }
}