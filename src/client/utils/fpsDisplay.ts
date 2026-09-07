import { Text, Application, Graphics } from "pixi.js";
import { NetworkManager } from "../network/networkManager";

export class FpsDisplay {
    private fpsText: Text;
    private statsText: Text;
    private background: Graphics;
    private app: Application;
    private networkManager: NetworkManager;
    private showDetailedStats: boolean = true;
    private statsUpdateCounter: number = 0;

    private messagesSent: number = 0;
    private messagesReceived: number = 0;
    private connectionStartTime: number = Date.now();
    private pingHistory: number[] = [];
    private lastPingTime: number = 0;

    constructor(app: Application, networkManager: NetworkManager) {
        this.app = app;
        this.networkManager = networkManager;

        console.log("Initializing FPS Display...");

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

        this.background = new Graphics();
        this.background.fill(0x000000);
        this.background.alpha = 0.7;
        this.background.visible = this.showDetailedStats;
        app.stage.addChild(this.background);
        app.stage.addChild(this.statsText);

        this.statsText.visible = this.showDetailedStats;

        this.fpsText.visible = !this.showDetailedStats;

        this.setupNetworkTracking();

    }

    private setupNetworkTracking() {

        setTimeout(() => {
            try {

                const socket = this.networkManager['socket'];
                if (socket && socket.send) {
                    const originalSend = socket.send.bind(socket);
                    socket.send = (data: string | ArrayBufferLike | Blob | ArrayBufferView) => {
                        this.messagesSent++;
                        return originalSend(data);
                    };
                }

                const originalHandleMessage = this.networkManager['handleServerMessage'];
                if (originalHandleMessage) {
                    this.networkManager['handleServerMessage'] = (data: string | ArrayBuffer) => {
                        this.messagesReceived++;
                        return originalHandleMessage.call(this.networkManager, data);
                    };
                }
            } catch (error) {
                console.warn('Failed to setup network tracking:', error);
            }
        }, 100);

        this.networkManager.onLatency((latencyMs) => {
            this.addPingMeasurement(latencyMs);
        });
    }

    private addPingMeasurement(ping: number) {
        this.pingHistory.push(ping);
        this.lastPingTime = Date.now();
        if (this.pingHistory.length > 20) {
            this.pingHistory.shift();
        }
    }

    update() {

        if (!this.showDetailedStats) {
            this.fpsText.text = `FPS: ${Math.round(this.app.ticker.FPS)}`;
            this.fpsText.visible = true;
        } else {

            this.fpsText.visible = false;
        }

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
        const currentPlayerId = this.networkManager.getPlayerId();

        const visiblePlayers = Object.keys(players).filter(id => id !== currentPlayerId).length;
        const totalPlayers = Object.keys(players).length;

        const pingDisplay = this.getPingDisplayText();

        const stats = [
            `=== GAME MONITORING ===`,
            `FPS: ${Math.round(this.app.ticker.FPS)}`,
            `Frame Time: ${(1000 / this.app.ticker.FPS).toFixed(2)}ms`,
            ``,
            `=== NETWORK ===`,
            `Status: ${this.getConnectionStatus()}`,
            `Ping: ${pingDisplay}`,
            `Messages Sent: ${this.messagesSent}`,
            `Messages Received: ${this.messagesReceived}`,
            `Players Visible: ${visiblePlayers}`,
            `Total Players: ${totalPlayers}`,
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
            `Player ID: ${currentPlayerId || 'Connecting...'}`,
            `Uptime: ${this.formatTime(now - this.connectionStartTime)}`
        ];

        this.statsText.text = stats.join('\n');

        this.background.clear();
        this.background.fill(0x000000);
        this.background.alpha = 0.8;
        const bounds = this.statsText.getBounds();
        this.background.rect(bounds.x - 5, bounds.y - 5, bounds.width + 10, bounds.height + 10);
    }

    private calculateAveragePing(): number {
        if (this.pingHistory.length === 0) return 0;

        const sum = this.pingHistory.reduce((a, b) => a + b, 0);
        const average = sum / this.pingHistory.length;

        return Math.round(average);
    }

    private getPingDisplayText(): string {
        if (this.pingHistory.length === 0) {
            return "Measuring...";
        }

        const ping = this.calculateAveragePing();
        const timeSinceLastPing = Date.now() - this.lastPingTime;

        if (timeSinceLastPing > 10000) {
            return `${ping}ms (${Math.floor(timeSinceLastPing / 1000)}s ago)`;
        }

        return `${ping}ms`;
    }    private getConnectionStatus(): string {
        return this.networkManager.getConnectionStatus();
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

        this.fpsText.visible = !this.showDetailedStats;

        if (this.showDetailedStats) {
            this.updateDetailedStats();
        }
    }

    isDetailedStatsVisible(): boolean {
        return this.showDetailedStats;
    }

    destroy() {
    }
}
