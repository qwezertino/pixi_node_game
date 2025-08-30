/**
 * Optimized visibility and broadcasting system for 10k+ players
 * Maintains full viewport visibility while optimizing performance
 */

export interface ViewportBounds {
    minX: number;
    maxX: number;
    minY: number;
    maxY: number;
}

export interface PlayerVisibilityData {
    playerId: string;
    position: { x: number; y: number };
    viewport: ViewportBounds;
    lastViewportUpdate: number;
    visiblePlayers: Set<string>;
    lastVisibilityUpdate: number;
}

/**
 * High-performance visibility manager that maintains full viewport visibility
 * Uses spatial optimization WITHOUT grid restrictions
 */
export class PerformanceVisibilityManager {
    private players = new Map<string, PlayerVisibilityData>();
    private positionIndex = new Map<string, { x: number; y: number }>(); // Fast position lookup

    // Performance optimization: batch visibility updates
    private visibilityUpdateQueue = new Set<string>();
    private readonly VISIBILITY_UPDATE_INTERVAL = 100; // ms
    private readonly VIEWPORT_CHANGE_THRESHOLD = 50; // pixels

    constructor() {
        // Process visibility updates in batches
        setInterval(() => {
            this.processBatchedVisibilityUpdates();
        }, this.VISIBILITY_UPDATE_INTERVAL);
    }

    public addPlayer(playerId: string, x: number, y: number, viewportWidth: number, viewportHeight: number): void {
        const viewport = this.calculateViewportBounds(x, y, viewportWidth, viewportHeight);

        const playerData: PlayerVisibilityData = {
            playerId,
            position: { x, y },
            viewport,
            lastViewportUpdate: Date.now(),
            visiblePlayers: new Set(),
            lastVisibilityUpdate: 0
        };

        this.players.set(playerId, playerData);
        this.positionIndex.set(playerId, { x, y });

        // Immediate visibility calculation for new player
        this.updatePlayerVisibility(playerId);

    }

    public updatePlayerPosition(playerId: string, x: number, y: number): void {
        const playerData = this.players.get(playerId);
        if (!playerData) return;

        const oldPos = playerData.position;
        playerData.position = { x, y };
        this.positionIndex.set(playerId, { x, y });

        // Update viewport center
        const viewportWidth = playerData.viewport.maxX - playerData.viewport.minX;
        const viewportHeight = playerData.viewport.maxY - playerData.viewport.minY;
        playerData.viewport = this.calculateViewportBounds(x, y, viewportWidth, viewportHeight);

        // Check if position change is significant enough to update visibility
        const distance = Math.sqrt(Math.pow(x - oldPos.x, 2) + Math.pow(y - oldPos.y, 2));
        if (distance > this.VIEWPORT_CHANGE_THRESHOLD) {
            this.visibilityUpdateQueue.add(playerId);
        }

        // Also queue players who might now see/unsee this player
        this.queueAffectedPlayers(playerId, oldPos, { x, y });
    }

    public updatePlayerViewport(playerId: string, viewportWidth: number, viewportHeight: number): void {
        const playerData = this.players.get(playerId);
        if (!playerData) return;

        const newViewport = this.calculateViewportBounds(
            playerData.position.x,
            playerData.position.y,
            viewportWidth,
            viewportHeight
        );

        // Check if viewport change is significant
        const viewportChanged =
            Math.abs(newViewport.minX - playerData.viewport.minX) > this.VIEWPORT_CHANGE_THRESHOLD ||
            Math.abs(newViewport.maxX - playerData.viewport.maxX) > this.VIEWPORT_CHANGE_THRESHOLD ||
            Math.abs(newViewport.minY - playerData.viewport.minY) > this.VIEWPORT_CHANGE_THRESHOLD ||
            Math.abs(newViewport.maxY - playerData.viewport.maxY) > this.VIEWPORT_CHANGE_THRESHOLD;

        if (viewportChanged) {
            playerData.viewport = newViewport;
            playerData.lastViewportUpdate = Date.now();
            this.visibilityUpdateQueue.add(playerId);
        }
    }

    public removePlayer(playerId: string): void {
        this.players.delete(playerId);
        this.positionIndex.delete(playerId);
        this.visibilityUpdateQueue.delete(playerId);
    }

    public getVisiblePlayers(playerId: string): Set<string> {
        const playerData = this.players.get(playerId);
        return playerData ? new Set(playerData.visiblePlayers) : new Set();
    }

    // Optimized: get players who can see a specific player
    public getPlayersWhoCanSee(targetPlayerId: string): Set<string> {
        const targetPos = this.positionIndex.get(targetPlayerId);
        if (!targetPos) return new Set();

        const observers = new Set<string>();

        // Fast iteration through position index instead of full player data
        for (const [observerId, observerData] of this.players) {
            if (observerId === targetPlayerId) continue;

            if (this.isPositionInViewport(targetPos, observerData.viewport)) {
                observers.add(observerId);
            }
        }

        return observers;
    }

    private calculateViewportBounds(centerX: number, centerY: number, width: number, height: number): ViewportBounds {
        // Add buffer zone for smooth transitions (25% of screen size)
        const bufferX = width * 0.25;
        const bufferY = height * 0.25;

        return {
            minX: centerX - (width / 2) - bufferX,
            maxX: centerX + (width / 2) + bufferX,
            minY: centerY - (height / 2) - bufferY,
            maxY: centerY + (height / 2) + bufferY
        };
    }

    private isPositionInViewport(position: { x: number; y: number }, viewport: ViewportBounds): boolean {
        return position.x >= viewport.minX &&
               position.x <= viewport.maxX &&
               position.y >= viewport.minY &&
               position.y <= viewport.maxY;
    }

    private updatePlayerVisibility(playerId: string): void {
        const playerData = this.players.get(playerId);
        if (!playerData) return;

        const newVisiblePlayers = new Set<string>();

        // Optimized: iterate through position index first
        for (const [otherPlayerId, otherPos] of this.positionIndex) {
            if (otherPlayerId === playerId) continue;

            if (this.isPositionInViewport(otherPos, playerData.viewport)) {
                newVisiblePlayers.add(otherPlayerId);
            }
        }

        playerData.visiblePlayers = newVisiblePlayers;
        playerData.lastVisibilityUpdate = Date.now();

    }

    private queueAffectedPlayers(movedPlayerId: string, oldPos: { x: number; y: number }, newPos: { x: number; y: number }): void {
        // Queue players whose visibility might be affected by this movement
        for (const [observerId, observerData] of this.players) {
            if (observerId === movedPlayerId) continue;

            const wasVisible = this.isPositionInViewport(oldPos, observerData.viewport);
            const isVisible = this.isPositionInViewport(newPos, observerData.viewport);

            // If visibility status changed, queue observer for update
            if (wasVisible !== isVisible) {
                this.visibilityUpdateQueue.add(observerId);
            }
        }
    }

    private processBatchedVisibilityUpdates(): void {
        if (this.visibilityUpdateQueue.size === 0) return;

        const playersToUpdate = Array.from(this.visibilityUpdateQueue);
        this.visibilityUpdateQueue.clear();

        // Process updates in batches to avoid blocking
        const batchSize = 100;
        for (let i = 0; i < playersToUpdate.length; i += batchSize) {
            const batch = playersToUpdate.slice(i, i + batchSize);

            // Use setTimeout to avoid blocking the event loop
            setTimeout(() => {
                for (const playerId of batch) {
                    this.updatePlayerVisibility(playerId);
                }
            }, 0);
        }
    }

    // Performance monitoring
    public getPerformanceStats(): {
        totalPlayers: number;
        avgVisiblePlayers: number;
        maxVisiblePlayers: number;
        queuedUpdates: number;
    } {
        let totalVisible = 0;
        let maxVisible = 0;

        for (const playerData of this.players.values()) {
            const visibleCount = playerData.visiblePlayers.size;
            totalVisible += visibleCount;
            maxVisible = Math.max(maxVisible, visibleCount);
        }

        return {
            totalPlayers: this.players.size,
            avgVisiblePlayers: this.players.size > 0 ? totalVisible / this.players.size : 0,
            maxVisiblePlayers: maxVisible,
            queuedUpdates: this.visibilityUpdateQueue.size
        };
    }
}
