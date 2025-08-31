import { Text, Application, Graphics } from "pixi.js";
import { NetworkManager } from "../network/networkManager";

export class FpsDisplay {
    private fpsText: Text;
    private statsText: Text;
    private background: Graphics;
    private app: Application;
    private networkManager: NetworkManager;
    private showDetailedStats: boolean = true; // Изначально показываем полные метрики
    private statsUpdateCounter: number = 0;

    // Network stats tracking
    private messagesSent: number = 0;
    private messagesReceived: number = 0;
    private connectionStartTime: number = Date.now();
    private pingHistory: number[] = [];
    private pendingPings: Map<number, number> = new Map(); // inputSequence -> sendTime
    private pingInterval: number | null = null;
    private lastPingTime: number = 0; // Время последнего измерения пинга

    constructor(app: Application, networkManager: NetworkManager) {
        this.app = app;
        this.networkManager = networkManager;

        console.log("Initializing FPS Display...");

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
        this.background.visible = this.showDetailedStats; // Показываем если включены детальные статистики
        app.stage.addChild(this.background);
        app.stage.addChild(this.statsText);

        // Устанавливаем видимость статистики согласно начальному состоянию
        this.statsText.visible = this.showDetailedStats;

        // В детальном режиме скрываем простой FPS
        this.fpsText.visible = !this.showDetailedStats;

        // Track network messages
        this.setupNetworkTracking();

        // Start periodic ping for better latency measurement
        this.startPingInterval();
    }

    private startPingInterval() {
        // Не отправляем искусственные ping-сообщения
        // Вместо этого будем измерять пинг на основе реальных движений
        // Если игрок долго не двигается, покажем последний известный пинг
    }

    private setupNetworkTracking() {
        // Добавляем задержку, чтобы WebSocket был инициализирован
        setTimeout(() => {
            try {
                // Hook into WebSocket to track messages
                const socket = this.networkManager['socket'];
                if (socket && socket.send) {
                    const originalSend = socket.send.bind(socket);
                    socket.send = (data: string | ArrayBufferLike | Blob | ArrayBufferView) => {
                        this.messagesSent++;
                        return originalSend(data);
                    };
                }

                // Track received messages through existing handler
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

        // Clean up old pending pings (older than 5 seconds)
        const now = Date.now();
        for (const [seq, time] of this.pendingPings.entries()) {
            if (now - time > 5000) {
                this.pendingPings.delete(seq);
            }
        }
    }

    private addPingMeasurement(ping: number) {
        this.pingHistory.push(ping);
        this.lastPingTime = Date.now(); // Запоминаем время последнего измерения
        if (this.pingHistory.length > 20) { // Keep last 20 measurements
            this.pingHistory.shift();
        }
    }

    update() {
        // В минимальном режиме показываем только FPS
        if (!this.showDetailedStats) {
            this.fpsText.text = `FPS: ${Math.round(this.app.ticker.FPS)}`;
            this.fpsText.visible = true;
        } else {
            // В детальном режиме скрываем простой FPS, так как он будет в детальных статистиках
            this.fpsText.visible = false;
        }

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
        const currentPlayerId = this.networkManager.getPlayerId();

        // Count visible players (excluding current player)
        const visiblePlayers = Object.keys(players).filter(id => id !== currentPlayerId).length;
        const totalPlayers = Object.keys(players).length;

        // Calculate ping display
        const pingDisplay = this.getPingDisplayText();

        // Build stats string
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

        // Update background size
        this.background.clear();
        this.background.fill(0x000000);
        this.background.alpha = 0.8;
        const bounds = this.statsText.getBounds();
        this.background.rect(bounds.x - 5, bounds.y - 5, bounds.width + 10, bounds.height + 10);
    }

    private calculateAveragePing(): number {
        if (this.pingHistory.length === 0) return 0;

        // Calculate average of recent ping measurements
        const sum = this.pingHistory.reduce((a, b) => a + b, 0);
        const average = sum / this.pingHistory.length;

        // Return rounded average
        return Math.round(average);
    }

    private getPingDisplayText(): string {
        if (this.pingHistory.length === 0) {
            // Если еще не было измерений пинга
            return "Waiting for movement...";
        }

        const ping = this.calculateAveragePing();
        const timeSinceLastPing = Date.now() - this.lastPingTime;

        // Если последнее измерение было больше 10 секунд назад
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

        // Управляем видимостью детальных статистик
        this.statsText.visible = this.showDetailedStats;
        this.background.visible = this.showDetailedStats;

        // Управляем видимостью простого FPS
        this.fpsText.visible = !this.showDetailedStats;

        if (this.showDetailedStats) {
            this.updateDetailedStats(); // Immediate update when shown
        }
    }

    isDetailedStatsVisible(): boolean {
        return this.showDetailedStats;
    }

    // Cleanup method
    destroy() {
        // Больше не используем pingInterval, но оставляем для совместимости
        if (this.pingInterval) {
            window.clearInterval(this.pingInterval);
            this.pingInterval = null;
        }
    }
}