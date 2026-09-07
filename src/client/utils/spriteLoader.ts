import { Assets, AnimatedSprite, Texture, Rectangle } from "pixi.js";
import type { UnitDefinition } from "../../shared/units";
import {
    getNonCombatFrameRects,
    getCombatFrameRects,
    gridFor,
    recommendedScale,
    DIRECTIONS,
    type FrameRect,
} from "./animationLayout";
import { PLAYER } from "../../shared/gameConfig";

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

export interface CharacterVisual {
    /**
     * Keyed by animation name for the placeholder sheet (directional=false: "idle",
     * "run", "attack", ...), or by "<name>_<direction>" for real per-unit sheets
     * (directional=true: "idle_right", "attack_down", ...) — see animationLayout.ts.
     */
    animations: Map<string, Texture[]>;
    getAnimation: (name: string) => AnimatedSprite | undefined;
    /** sprite.scale to apply so this unit renders at TARGET_ONSCREEN_SIZE on screen. */
    scale: number;
    /**
     * false: only left/right facing exists, done by flipping sprite.scale.x (the
     * placeholder sheet). true: distinct art per direction, looked up by key — no
     * flipping, see AnimationController.
     */
    directional: boolean;
}

export class SpriteLoader {
    private static readonly characterVisuals = new Map<string, Promise<CharacterVisual>>();

    static loadCharacterVisual(spritesheetPath: string): Promise<CharacterVisual> {
        let visual = this.characterVisuals.get(spritesheetPath);
        if (!visual) {
            visual = this.createCharacterVisual(spritesheetPath);
            this.characterVisuals.set(spritesheetPath, visual);
            visual.catch(() => this.characterVisuals.delete(spritesheetPath));
        }
        return visual;
    }

    private static async createCharacterVisual(spritesheetPath: string): Promise<CharacterVisual> {
        const sheetTexture = await Assets.load(spritesheetPath);
        sheetTexture.source.scaleMode = "nearest";
        const animations = new Map<string, Texture[]>();

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
            },

            scale: PLAYER.baseScale,
            directional: false,
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

    private static readonly unitVisuals = new Map<string, Promise<CharacterVisual>>();

    static loadUnitCharacterVisual(unit: UnitDefinition): Promise<CharacterVisual> {
        let visual = this.unitVisuals.get(unit.id);
        if (!visual) {
            visual = this.createUnitCharacterVisual(unit);
            this.unitVisuals.set(unit.id, visual);
            visual.catch(() => this.unitVisuals.delete(unit.id));
        }
        return visual;
    }

    private static async createUnitCharacterVisual(unit: UnitDefinition): Promise<CharacterVisual> {
        if (!unit.assetPath) {
            throw new Error(`unit ${unit.id} has no assetPath`);
        }

        const movementTexture = await Assets.load(`/assets/${unit.assetPath}`);
        movementTexture.source.scaleMode = "nearest";
        const animations = new Map<string, Texture[]>();

        for (const direction of DIRECTIONS) {
            const idleRects = getNonCombatFrameRects(movementTexture.width, movementTexture.height, "idle", direction);
            const walkRects = getNonCombatFrameRects(movementTexture.width, movementTexture.height, "walk", direction);
            const runRects = getNonCombatFrameRects(movementTexture.width, movementTexture.height, "run", direction);
            if (idleRects) animations.set(`idle_${direction}`, this.framesFromRects(movementTexture, idleRects));
            if (walkRects) animations.set(`walk_${direction}`, this.framesFromRects(movementTexture, walkRects));
            if (runRects) animations.set(`run_${direction}`, this.framesFromRects(movementTexture, runRects));
        }
        if (!animations.has("idle_right") || !animations.has("run_right")) {
            throw new Error(`unit ${unit.id}: movement sheet layout unrecognized (${unit.assetPath})`);
        }
        for (const direction of DIRECTIONS) {
            if (!animations.has(`walk_${direction}`)) {

                animations.set(`walk_${direction}`, animations.get(`run_${direction}`)!);
            }
        }

        if (unit.combatAssetPath) {
            const combatTexture = await Assets.load(`/assets/${unit.combatAssetPath}`);
            combatTexture.source.scaleMode = "nearest";
            for (const direction of DIRECTIONS) {
                for (const variant of ["attack1", "attack2"] as const) {
                    const attackRects = getCombatFrameRects(combatTexture.width, combatTexture.height, variant, direction);
                    if (attackRects) animations.set(`${variant}_${direction}`, this.framesFromRects(combatTexture, attackRects));
                }
                const readyRects = getCombatFrameRects(combatTexture.width, combatTexture.height, "ready", direction);
                if (readyRects) animations.set(`ready_${direction}`, this.framesFromRects(combatTexture, readyRects));
            }
        }
        for (const direction of DIRECTIONS) {
            for (const variant of ["attack1", "attack2", "ready"] as const) {
                if (!animations.has(`${variant}_${direction}`)) {

                    animations.set(`${variant}_${direction}`, animations.get(`idle_${direction}`)!);
                }
            }
        }

        const cellSize = gridFor(movementTexture.width, movementTexture.height).cellSize;

        return {
            animations,
            getAnimation: (name: string) => {
                const textures = animations.get(name);
                if (!textures) return undefined;
                const anim = new AnimatedSprite(textures);
                anim.anchor.set(0.5);
                return anim;
            },
            scale: recommendedScale(cellSize, unit.id),
            directional: true,
        };
    }

    private static framesFromRects(texture: Texture, rects: FrameRect[]): Texture[] {
        return rects.map(
            (r) => new Texture({ source: texture.source, frame: new Rectangle(r.x, r.y, r.width, r.height) })
        );
    }
}
