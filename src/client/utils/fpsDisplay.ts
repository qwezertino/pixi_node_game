import { Text, Application, Graphics } from "pixi.js";
import { NetworkManager } from "../network/networkManager";

export class FpsDisplay {
    private fpsText: Text;
    private statsText: Text;
    private background: Graphics;
    private app: Application;
    private networkManager: NetworkManager;
    private showDetailedStats: boolean = false;
    private statsUpdateCounter: number = 0;

    // Network stats tracking
    private messagesSent: number = 0;
    private messagesReceived: number = 0;
    private connectionStartTime: number = Date.now();
    private pingHistory: number[] = [];
    private pendingPings: Map<number, number> = new Map(); // inputSequence -> sendTime

    constructor(app: Application, networkManager: NetworkManager) {
        this.app = app;
        this.networkManager = networkManager;

        // Create FPS text (always visible)
        this.fpsText = new Text({
            text: "FPS: 0",
            style: {
                fontFamily: "Arial",
                fontSize: 16,
                fill: 0xffffff,
                align: "left"
            }
        });
        this.fpsText.position.set(10, 10);
        app.stage.addChild(this.fpsText);

        // Create detailed stats text (hidden by default)
        this.statsText = new Text({
            text: "",
            style: {
                fontFamily: "Courier New",
                fontSize: 12,
                fill: 0xffffff,
                align: "left"
            }
        });
        this.statsText.position.set(10, 35);

        // Create background for stats
        this.background = new Graphics();
        this.background.fill(0x000000);
        this.background.alpha = 0.7;
        this.background.visible = false;
        app.stage.addChild(this.background);
        app.stage.addChild(this.statsText);

        // Track network messages
        this.setupNetworkTracking();
    }

    private setupNetworkTracking() {
        // Hook into WebSocket to track messages
        const originalSend = this.networkManager['socket'].send;
        this.networkManager['socket'].send = (data: string | ArrayBufferLike | Blob | ArrayBufferView<ArrayBufferLike>) => {
            this.messagesSent++;
            return originalSend.call(this.networkManager['socket'], data);
        };

        // Track received messages through existing handler
        const originalHandleMessage = this.networkManager['handleServerMessage'];
        this.networkManager['handleServerMessage'] = (data: string | ArrayBuffer) => {
            this.messagesReceived++;
            return originalHandleMessage.call(this.networkManager, data);
        };

        // Track movement acknowledgments for ping calculation
        this.networkManager.onMovementAck((_, inputSequence) => {
            const sendTime = this.pendingPings.get(inputSequence);
            if (sendTime) {
                const ping = Date.now() - sendTime;
                this.addPingMeasurement(ping);
                this.pendingPings.delete(inputSequence);
            }
        });
    }

    public trackMovementSend(inputSequence: number) {
        this.pendingPings.set(inputSequence, Date.now());
    }

    private addPingMeasurement(ping: number) {
        this.pingHistory.push(ping);
        if (this.pingHistory.length > 20) { // Keep last 20 measurements
            this.pingHistory.shift();
        }
    }

    update() {
        // Update FPS (always visible)
        this.fpsText.text = `FPS: ${Math.round(this.app.ticker.FPS)}`;

        // Update detailed stats less frequently (every 10 frames)
        this.statsUpdateCounter++;
        if (this.statsUpdateCounter >= 10) {
            this.statsUpdateCounter = 0;
            this.updateDetailedStats();
        }
    }

    private updateDetailedStats() {
        if (!this.showDetailedStats) return;

        const now = Date.now();
        const memory = (performance as any).memory;
        const players = this.networkManager.getPlayers();
        const playerCount = Object.keys(players).length;

        // Calculate ping (simplified - in real implementation you'd use server timestamps)
        const ping = this.calculateAveragePing();

        // Build stats string
        const stats = [
            `=== GAME MONITORING ===`,
            `FPS: ${Math.round(this.app.ticker.FPS)}`,
            `Frame Time: ${(1000 / this.app.ticker.FPS).toFixed(2)}ms`,
            ``,
            `=== NETWORK ===`,
            `Status: ${this.getConnectionStatus()}`,
            `Ping: ${ping}ms`,
            `Messages Sent: ${this.messagesSent}`,
            `Messages Received: ${this.messagesReceived}`,
            `Players Online: ${playerCount}`,
            ``,
            `=== MEMORY ===`,
            `Used: ${memory ? this.formatBytes(memory.usedJSHeapSize) : 'N/A'}`,
            `Total: ${memory ? this.formatBytes(memory.totalJSHeapSize) : 'N/A'}`,
            `Limit: ${memory ? this.formatBytes(memory.jsHeapSizeLimit) : 'N/A'}`,
            ``,
            `=== SYSTEM ===`,
            `Screen: ${window.innerWidth}x${window.innerHeight}`,
            `Device Pixel Ratio: ${window.devicePixelRatio}`,
            `User Agent: ${navigator.userAgent.substring(0, 50)}...`,
            ``,
            `=== GAME WORLD ===`,
            `Player ID: ${this.networkManager.getPlayerId() || 'Connecting...'}`,
            `Uptime: ${this.formatTime(now - this.connectionStartTime)}`
        ];

        this.statsText.text = stats.join('\n');

        // Update background size
        this.background.clear();
        this.background.fill(0x000000);
        this.background.alpha = 0.8;
        const bounds = this.statsText.getBounds();
        this.background.rect(bounds.x - 5, bounds.y - 5, bounds.width + 10, bounds.height + 10);
    }

    private calculateAveragePing(): number {
        // Simplified ping calculation - in production you'd measure actual round-trip time
        if (this.pingHistory.length === 0) return 0;
        const sum = this.pingHistory.reduce((a, b) => a + b, 0);
        return Math.round(sum / this.pingHistory.length);
    }

    private getConnectionStatus(): string {
        const socket = this.networkManager['socket'];
        switch (socket.readyState) {
            case WebSocket.CONNECTING: return 'Connecting';
            case WebSocket.OPEN: return 'Connected';
            case WebSocket.CLOSING: return 'Closing';
            case WebSocket.CLOSED: return 'Disconnected';
            default: return 'Unknown';
        }
    }

    private formatBytes(bytes: number): string {
        if (bytes === 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    private formatTime(ms: number): string {
        const seconds = Math.floor(ms / 1000);
        const minutes = Math.floor(seconds / 60);
        const hours = Math.floor(minutes / 60);

        if (hours > 0) {
            return `${hours}h ${minutes % 60}m ${seconds % 60}s`;
        } else if (minutes > 0) {
            return `${minutes}m ${seconds % 60}s`;
        } else {
            return `${seconds}s`;
        }
    }

    toggleDetailedStats() {
        this.showDetailedStats = !this.showDetailedStats;
        this.statsText.visible = this.showDetailedStats;
        this.background.visible = this.showDetailedStats;

        if (this.showDetailedStats) {
            this.updateDetailedStats(); // Immediate update when shown
        }
    }

    isDetailedStatsVisible(): boolean {
        return this.showDetailedStats;
    }
}