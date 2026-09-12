import { Container, Graphics } from "pixi.js";
import { CoordinateConverter } from "../utils/coordinateConverter";
import { getStructureColliders, onStructuresChanged } from "../collision/collisionState";

const STONE_WALL_COLOR = 0x8a8a8a;

/**
 * Renders runtime structure colliders (walls, gates, towers...) as simple
 * stone-colored rectangles, exactly matching their collider bounds. See
 * docs/collisions_plan.md, "Клиент и API": render bounds must line up with
 * collision bounds, and map/structure rendering are separate concerns from
 * the collider index itself.
 */
export class StructureRenderer {
    private readonly graphics = new Graphics();

    constructor(
        container: Container,
        private coordinateConverter: CoordinateConverter,
    ) {
        container.addChild(this.graphics);
        onStructuresChanged(() => this.redraw());
        this.redraw();
    }

    redraw(): void {
        this.graphics.clear();
        const scale = this.coordinateConverter.getScale();

        for (const c of getStructureColliders()) {
            const topLeft = this.coordinateConverter.virtualToScreen(c.minX, c.minY);
            const width = (c.maxX - c.minX) * scale.x;
            const height = (c.maxY - c.minY) * scale.y;

            this.graphics.rect(topLeft.x, topLeft.y, width, height);
            this.graphics.fill(STONE_WALL_COLOR);
        }
    }
}
