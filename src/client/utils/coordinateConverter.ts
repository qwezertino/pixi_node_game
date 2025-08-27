import { WORLD } from "../../common/gameSettings";

/**
 * Converts between virtual world coordinates and screen pixel coordinates
 * This ensures all players see the same relative positions regardless of screen size
 */
export class CoordinateConverter {
    private worldToScreenScaleX: number;
    private worldToScreenScaleY: number;
    private screenWidth: number;
    private screenHeight: number;

    constructor(screenWidth: number, screenHeight: number) {
        this.screenWidth = screenWidth;
        this.screenHeight = screenHeight;

        // Calculate scaling factors to fit world into screen
        this.worldToScreenScaleX = screenWidth / WORLD.BOUNDARIES.MAX_X;
        this.worldToScreenScaleY = screenHeight / WORLD.BOUNDARIES.MAX_Y;
    }

    /**
     * Convert world coordinates to screen pixels
     */
    worldToScreen(worldX: number, worldY: number): { x: number, y: number } {
        return {
            x: worldX * this.worldToScreenScaleX,
            y: worldY * this.worldToScreenScaleY
        };
    }

    /**
     * Convert screen pixels to world coordinates
     */
    screenToWorld(screenX: number, screenY: number): { x: number, y: number } {
        return {
            x: screenX / this.worldToScreenScaleX,
            y: screenY / this.worldToScreenScaleY
        };
    }

    /**
     * Get the center of the screen in world coordinates
     */
    getWorldCenter(): { x: number, y: number } {
        return this.screenToWorld(this.screenWidth / 2, this.screenHeight / 2);
    }

    /**
     * Update screen dimensions if window is resized
     */
    updateScreenSize(screenWidth: number, screenHeight: number) {
        this.screenWidth = screenWidth;
        this.screenHeight = screenHeight;
        this.worldToScreenScaleX = screenWidth / WORLD.BOUNDARIES.MAX_X;
        this.worldToScreenScaleY = screenHeight / WORLD.BOUNDARIES.MAX_Y;
    }
}
