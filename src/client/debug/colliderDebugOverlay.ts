import { Container, Graphics, Text } from "pixi.js";
import { CoordinateConverter } from "../utils/coordinateConverter";
import { mapStaticGrid } from "../../shared/mapColliders";
import { getStructureColliders, onStructuresChanged, currentStructureRevision } from "../collision/collisionState";
import { PLAYER_RADIUS } from "../collision/geometry";

const MAP_COLOR = 0x33cc33;
const STRUCTURE_COLOR = 0xff8800;
const PLAYER_COLOR = 0x33aaff;

// The world is rendered zoomed all the way out (the whole 3.2x3.2 km map
// fits on one screen), so PLAYER_RADIUS's true on-screen size is a fraction
// of a pixel. This is a debug view, not gameplay rendering, so it's fine to
// floor the drawn radius to something actually visible/clickable rather
// than being scale-accurate like the map/structure boxes.
const MIN_PLAYER_DEBUG_RADIUS_PX = 6;

export interface DebugPlayerCircle {
    id: string;
    x: number;
    y: number;
}

/**
 * Dev-only outline overlay over map/structure colliders — shows exactly
 * what the collision solver sees (ID, bounds, structure revision),
 * independent of the normal wall rendering. Off by default; toggled by
 * mountColliderOverlayButton. See docs/collisions_plan.md, "Наблюдаемость
 * и документация" (development overlay).
 */
export class ColliderDebugOverlay {
    // Map/structure outlines change rarely (resize, structure delta) — kept
    // on their own Graphics/labels so redrawing them doesn't compete with
    // the once-a-frame player layer below.
    private readonly staticGraphics = new Graphics();
    private readonly staticLabels = new Container();
    // Player circles move every tick, so they get their own Graphics/labels
    // redrawn every visible frame via drawPlayers(), independent of redraw().
    private readonly playerGraphics = new Graphics();
    private readonly playerLabels = new Container();
    private isVisible = false;

    constructor(
        container: Container,
        private coordinateConverter: CoordinateConverter,
    ) {
        for (const child of [this.staticGraphics, this.staticLabels, this.playerGraphics, this.playerLabels]) {
            child.visible = false;
            container.addChild(child);
        }

        onStructuresChanged(() => {
            if (this.isVisible) this.redraw();
        });
    }

    get visible(): boolean {
        return this.isVisible;
    }

    toggle(): boolean {
        this.isVisible = !this.isVisible;
        for (const child of [this.staticGraphics, this.staticLabels, this.playerGraphics, this.playerLabels]) {
            child.visible = this.isVisible;
        }
        if (this.isVisible) this.redraw();
        return this.isVisible;
    }

    private drawBox(
        graphics: Graphics,
        labels: Container,
        minX: number,
        minY: number,
        maxX: number,
        maxY: number,
        color: number,
        label: string,
    ): void {
        const topLeft = this.coordinateConverter.virtualToScreen(minX, minY);
        const scale = this.coordinateConverter.getScale();
        const width = (maxX - minX) * scale.x;
        const height = (maxY - minY) * scale.y;

        graphics.rect(topLeft.x, topLeft.y, width, height);
        graphics.stroke({ width: 2, color });

        const text = new Text({ text: label, style: { fontFamily: "Courier New", fontSize: 11, fill: color } });
        text.position.set(topLeft.x + 2, topLeft.y + 2);
        labels.addChild(text);
    }

    /** Redraws the map/structure layer. Call after a resize or a structure change. */
    redraw(): void {
        this.staticGraphics.clear();
        this.staticLabels.removeChildren();

        for (let i = 0; i < mapStaticGrid.colliderCount; i++) {
            const c = mapStaticGrid.collider(i);
            this.drawBox(this.staticGraphics, this.staticLabels, c.minX, c.minY, c.maxX, c.maxY, MAP_COLOR, `map#${c.id}`);
        }

        const revision = currentStructureRevision();
        for (const c of getStructureColliders()) {
            this.drawBox(
                this.staticGraphics, this.staticLabels,
                c.minX, c.minY, c.maxX, c.maxY,
                STRUCTURE_COLOR, `s${c.structureId}/${c.partId} rev${revision}`,
            );
        }
    }

    /**
     * Redraws the dynamic player layer — every connected player's
     * PLAYER_RADIUS movement circle (local and remote alike), labeled by
     * ID. No-op while hidden; call once per rendered frame.
     */
    drawPlayers(players: Iterable<DebugPlayerCircle>): void {
        if (!this.isVisible) return;

        this.playerGraphics.clear();
        this.playerLabels.removeChildren();
        const scale = this.coordinateConverter.getScale();

        for (const p of players) {
            const center = this.coordinateConverter.virtualToScreen(p.x, p.y);
            const rx = Math.max(PLAYER_RADIUS * scale.x, MIN_PLAYER_DEBUG_RADIUS_PX);
            const ry = Math.max(PLAYER_RADIUS * scale.y, MIN_PLAYER_DEBUG_RADIUS_PX);

            this.playerGraphics.ellipse(center.x, center.y, rx, ry);
            this.playerGraphics.stroke({ width: 2, color: PLAYER_COLOR });

            const text = new Text({ text: `p${p.id}`, style: { fontFamily: "Courier New", fontSize: 11, fill: PLAYER_COLOR } });
            text.position.set(center.x + rx + 2, center.y - 6);
            this.playerLabels.addChild(text);
        }
    }

    /** Recompute screen positions after a resize, if currently shown. */
    onResize(): void {
        if (this.isVisible) this.redraw();
    }
}

/**
 * Mounts the dev-only "🧱 Colliders" toggle button next to the other debug
 * buttons (Units, Respawn).
 */
export function mountColliderOverlayButton(overlay: ColliderDebugOverlay): void {
    const button = document.createElement("button");
    const label = (on: boolean) => `🧱 Colliders: ${on ? "ON" : "OFF"}`;
    button.textContent = label(overlay.visible);
    button.title = "Toggle map/structure/player collider debug overlay (dev only)";
    button.style.cssText = `
        position: fixed; bottom: 12px; left: 230px; z-index: 10000;
        padding: 6px 12px; font-size: 12px; font-family: -apple-system, system-ui, sans-serif;
        background: #333; color: #eee; border: 1px solid #555; border-radius: 4px; cursor: pointer;
    `;

    button.addEventListener("click", () => {
        const on = overlay.toggle();
        button.textContent = label(on);
    });

    document.body.appendChild(button);
}
