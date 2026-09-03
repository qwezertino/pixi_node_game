import { BinaryProtocol } from "./protocol/binaryProtocol";
import {
    PlayerState,
    PlayerPosition,
    PROTOCOL_VERSION
} from "./protocol/messages";

// Beyond this the gap is a stall or a wrap, not a paced frame: extrapolating across it
// would fling every remote player across the map. Hold position and wait for a record.
const MAX_DEAD_RECKON_TICKS = 20;

// Callback types
export type OnPlayerJoinedCallback = (player: PlayerState) => void;
export type OnPlayerLeftCallback = (playerId: string) => void;
export type OnPlayerMovementCallback = (
    playerId: string,
    dx: number,
    dy: number
) => void;
export type OnPlayerDirectionCallback = (
    playerId: string,
    direction: -1 | 1
) => void;
export type OnGameStateCallback = (
    players: Record<string, PlayerState>,
    stateSequence: number | undefined,
    fullState: boolean,
    // Simulation ticks elapsed since the previous frame this client received.
    // Players missing from the frame advanced by exactly this many steps.
    elapsedTicks: number,
    // Server's current time-dilation factor as a percentage (100 = nominal).
    dilationPct: number
) => void;
export type OnCorrectionCallback = (position: PlayerPosition) => void;
export type OnMovementAckCallback = (position: PlayerPosition, inputSequence: number) => void;
export type OnSessionStartCallback = (position: PlayerPosition) => void;
export type OnLatencyCallback = (latencyMs: number) => void;
export type OnPlayerAttackCallback = (
    playerId: string,
    position: PlayerPosition
) => void;

export class NetworkManager {
    private socket: WebSocket | null = null;
    private worker: Worker | null = null;
    private useWorker: boolean = true; // Use Web Worker for WebSocket to avoid blocking main thread
    private connected = false;
    private playerId: string = "";
    private initialPosition: PlayerPosition = { x: 0, y: 0 };
    private players: Record<string, PlayerState> = {};
    private lastStateSequence: number = 0;
    private hasStateSequence = false;
    private resyncRequested = false;
    private resyncRetryTimer: number | null = null;
    private receivedInitialPosition = false;
    private lastWorldTick = 0;
    private hasWorldTick = false;
    // Server's current time-dilation factor (100 = nominal tick rate). main.ts scales
    // its fixed-timestep accumulator by this so local prediction stays in lockstep
    // with a server that has slowed its own simulation under pressure.
    private dilationPct = 100;

    // Reconnect state
    private wsUrl = "";
    private reconnectTimer: number | null = null;
    private reconnectAttempts = 0;
    private closedByClient = false;
    private protocolMismatch = false;
    private directPingTimer: number | null = null;
    private directPingNonce = 0;
    private directPendingPings = new Map<number, number>();

    // Callback handlers
    private onSessionStartCallbacks: OnSessionStartCallback[] = [];
    private onPlayerJoinedCallbacks: OnPlayerJoinedCallback[] = [];
    private onPlayerLeftCallbacks: OnPlayerLeftCallback[] = [];
    private onPlayerMovementCallbacks: OnPlayerMovementCallback[] = [];
    private onPlayerDirectionCallbacks: OnPlayerDirectionCallback[] = [];
    private onGameStateCallbacks: OnGameStateCallback[] = [];
    private onCorrectionCallbacks: OnCorrectionCallback[] = [];
    private onMovementAckCallbacks: OnMovementAckCallback[] = [];
    private onPlayerAttackCallbacks: OnPlayerAttackCallback[] = [];
    private onLatencyCallbacks: OnLatencyCallback[] = [];

    constructor() {
        if (this.useWorker && typeof Worker !== 'undefined') {
            this.initWorker();
        } else {
            this.initDirectSocket();
        }
    }

    private initWorker() {
        try {
            this.worker = new Worker(new URL('./networkWorker.ts', import.meta.url), { type: 'module' });

            this.worker.onmessage = (e) => {
                const msg = e.data;
                switch (msg.type) {
                    case 'open':
                        this.onSocketOpen();
                        break;
                    case 'message':
                        this.handleServerMessage(msg.data);
                        break;
                    case 'close':
                        this.onSocketClose();
                        break;
                    case 'error':
                        this.onSocketError();
                        break;
                    case 'latency':
                        if (typeof msg.latencyMs === 'number') {
                            this.emitLatency(msg.latencyMs);
                        }
                        break;
                }
            };

            this.worker.onerror = (error) => {
                console.error('Network Worker error:', error);
                // Fallback to direct socket
                this.worker?.terminate();
                this.worker = null;
                this.useWorker = false;
                this.initDirectSocket();
            };

            this.wsUrl = this.resolveWsUrl();
            this.worker.postMessage({ type: 'connect', url: this.wsUrl });
        } catch (error) {
            console.warn('Failed to initialize Web Worker, falling back to direct WebSocket:', error);
            this.useWorker = false;
            this.initDirectSocket();
        }
    }

    private initDirectSocket() {
        this.wsUrl = this.resolveWsUrl();

        this.socket = new WebSocket(this.wsUrl);
        this.socket.binaryType = "arraybuffer";
        this.setupSocketEvents();
    }

    private resolveWsUrl(): string {
        const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
        return `${protocol}//${window.location.host}/ws`;
    }

    private onSocketOpen() {
        this.connected = true;
        this.reconnectAttempts = 0;
        if (!this.worker) this.startDirectPings();
    }

    private onSocketClose() {
        this.connected = false;
        this.stopDirectPings();
        if (this.resyncRetryTimer !== null) {
            window.clearTimeout(this.resyncRetryTimer);
            this.resyncRetryTimer = null;
        }
        this.resyncRequested = false;

        // The server hands out a fresh player ID on every accept, so a reconnect is a
        // new session rather than a resumption. Everything keyed to the old identity
        // must go before the next WELCOME arrives.
        this.playerId = "";
        this.players = {};
        this.lastStateSequence = 0;
        this.hasStateSequence = false;
        this.receivedInitialPosition = false;
        this.lastWorldTick = 0;
        this.hasWorldTick = false;

        this.scheduleReconnect();
    }

    private scheduleReconnect() {
        if (this.closedByClient || this.reconnectTimer !== null) return;

        // Exponential backoff with jitter: a server restart would otherwise bring
        // every client back simultaneously and reproduce the connect storm.
        const base = Math.min(500 * 2 ** this.reconnectAttempts, 8000);
        const delay = base * (0.5 + Math.random() * 0.5);
        this.reconnectAttempts++;

        this.reconnectTimer = window.setTimeout(() => {
            this.reconnectTimer = null;
            if (this.closedByClient) return;

            if (this.worker) {
                this.worker.postMessage({ type: 'connect', url: this.wsUrl });
            } else {
                this.initDirectSocket();
            }
        }, delay);
    }

    private onSocketError() {
        // Handle connection error
        console.error('WebSocket error');
    }

    private isNewerStateSequence(next: number, current: number): boolean {
        if (current === 0) return true;
        if (next === current) return false;

        // Unsigned wrap-aware comparison for uint32 sequence numbers.
        const delta = (next - current) >>> 0;
        return delta < 0x80000000;
    }

    private setupSocketEvents() {
        if (!this.socket) return;

        // Connection established
        this.socket.addEventListener("open", () => this.onSocketOpen());

        // Receive messages from server
        this.socket.addEventListener("message", async (event) => {
            try {
                let processedData = event.data;

                // Handle Blob data
                if (processedData instanceof Blob) {
                    processedData = await processedData.arrayBuffer();
                }

                this.handleServerMessage(processedData);
            } catch (error) {
                console.error("Error processing server message:", error);
            }
        });

        // Connection closed
        this.socket.addEventListener("close", () => this.onSocketClose());

        // Connection error
        this.socket.addEventListener("error", () => {
            console.error('WebSocket error');
        });
    }

    private handleServerMessage(data: string | ArrayBuffer) {
        try {
            // Handle binary message
            if (data instanceof ArrayBuffer) {
                const message = BinaryProtocol.decodeMessage(
                    new Uint8Array(data)
                );

                if (!message) {
                    return;
                }

                switch (message.type) {
                    case "welcome":
                        if (message.protocolVersion !== PROTOCOL_VERSION) {
                            console.error(
                                `Protocol mismatch: server speaks v${message.protocolVersion}, ` +
                                `client speaks v${PROTOCOL_VERSION}. Refusing to decode world state.`
                            );
                            this.protocolMismatch = true;
                            this.closedByClient = true;
                            this.disconnect();
                            break;
                        }
                        this.protocolMismatch = false;
                        this.playerId = message.playerId;
                        break;

                    case "playerMovement":


                        if (
                            message.movementVector &&
                            message.playerId !== this.playerId
                        ) {
                            this.onPlayerMovementCallbacks.forEach((callback) =>
                                callback(
                                    message.playerId,
                                    message.movementVector.dx,
                                    message.movementVector.dy
                                )
                            );
                        }
                        break;

                    case "playerDirection":
                        // Only process direction updates for other players, not ourselves
                        if (message.playerId !== this.playerId) {
                            this.onPlayerDirectionCallbacks.forEach(
                                (callback) =>
                                    callback(
                                        message.playerId,
                                        message.direction
                                    )
                            );
                        }
                        break;

                    // case "initialState":
                    //     console.log("🌍 Received initialState:", message);
                    //     this.playerId = message.player.id;
                    //     this.initialPosition = message.player.position;
                    //     this.players = message.players;

                    //     console.log("📋 Player ID set to:", this.playerId);
                    //     console.log("📋 Initial position:", this.initialPosition);
                    //     console.log("📋 All players:", this.players);

                    //     // Notify about initial game state
                    //     this.onGameStateCallbacks.forEach((callback) =>
                    //         callback(message.players)
                    //     );
                    //     break;

                    case "playerJoined":
                        this.players[message.player.id] = message.player;
                        this.onPlayerJoinedCallbacks.forEach((callback) =>
                            callback(message.player)
                        );
                        break;

                    case "playerLeft":
                        delete this.players[message.playerId];
                        this.onPlayerLeftCallbacks.forEach((callback) =>
                            callback(message.playerId)
                        );
                        break;

                    case "gameState":
                    case "deltaGameState": {
                        const fullState = message.type === "gameState";
                        if (typeof message.stateSequence === "number") {
                            const sequence = message.stateSequence >>> 0;
                            if (this.hasStateSequence && !this.isNewerStateSequence(sequence, this.lastStateSequence)) {
                                break;
                            }

                            const distance = (sequence - this.lastStateSequence) >>> 0;
                            if (!fullState && this.hasStateSequence && distance !== 1) {
                                this.requestFullState();
                                break;
                            }
                            this.lastStateSequence = sequence;
                            this.hasStateSequence = true;
                            if (fullState) {
                                this.resyncRequested = false;
                                if (this.resyncRetryTimer !== null) {
                                    window.clearTimeout(this.resyncRetryTimer);
                                    this.resyncRetryTimer = null;
                                }
                            }
                        }

                        // Backward-compatible fallback for servers predating WELCOME.
                        if (!this.playerId && message.players) {
                            const playerIds = Object.keys(message.players);
                            if (playerIds.length > 0) {
                                this.playerId = playerIds[playerIds.length - 1];

                                if (message.players[this.playerId]) {
                                    this.initialPosition = message.players[this.playerId].position;
                                }
                            }
                        }

                        // Ticks elapsed since the previous frame *this client saw*, not
                        // since the previous frame the server sent: a frame the fanout
                        // shed must still leave dead reckoning on the right step.
                        const worldTick = (message.worldTick ?? 0) >>> 0;
                        let elapsedTicks = 0;
                        if (this.hasWorldTick) {
                            elapsedTicks = (worldTick - this.lastWorldTick) >>> 0;
                            if (elapsedTicks > MAX_DEAD_RECKON_TICKS) elapsedTicks = 0;
                        }
                        this.lastWorldTick = worldTick;
                        this.hasWorldTick = true;

                        const incomingPlayers = message.players as Record<string, PlayerState>;
                        const prevPlayers = this.players;

                        if (!this.receivedInitialPosition && this.playerId && incomingPlayers[this.playerId]) {
                            this.initialPosition = incomingPlayers[this.playerId].position;
                            this.receivedInitialPosition = true;
                            // Fires on the first snapshot of every session, including
                            // those that follow a reconnect with a new player ID.
                            this.onSessionStartCallbacks.forEach((callback) =>
                                callback(this.initialPosition)
                            );
                        }

                        // Fire animation callbacks based on state changes
                        Object.entries(incomingPlayers).forEach(([id, player]) => {
                            const isLocalPlayer = id === this.playerId;
                            const prev = prevPlayers[id];

                            // Movement: skip local player (handled by MovementController)
                            if (!isLocalPlayer && player.moving !== prev?.moving) {
                                this.onPlayerMovementCallbacks.forEach((cb) =>
                                    cb(id, player.vx ?? 0, player.vy ?? 0)
                                );
                            }
                            // Attack: include local player — server is authoritative, no prediction
                            if (player.attacking && !prev?.attacking) {
                                this.onPlayerAttackCallbacks.forEach((cb) =>
                                    cb(id, player.position)
                                );
                            }
                        });

                        if (fullState) {
                            this.players = incomingPlayers;
                        } else {
                            // Mutate only changed records. Copying the full 2000-player
                            // object for every small delta defeats delta compression.
                            for (const [id, player] of Object.entries(incomingPlayers)) {
                                this.players[id] = player;
                            }
                        }

                        this.dilationPct = message.dilationPct ?? 100;

                        this.onGameStateCallbacks.forEach((callback) =>
                            callback(incomingPlayers, message.stateSequence, fullState, elapsedTicks, this.dilationPct)
                        );
                        break;
                    }

                    case "movementAck":

                        if (message.playerId === this.playerId) {
                            this.onMovementAckCallbacks.forEach((callback) =>
                                callback(message.position, message.inputSequence)
                            );
                        }
                        break;

                    case "pong": {
                        const sentAt = this.directPendingPings.get(message.nonce);
                        if (sentAt !== undefined) {
                            this.directPendingPings.delete(message.nonce);
                            this.emitLatency(performance.now() - sentAt);
                        }
                        break;
                    }

                    // case "correction":
                    //     if (message.playerId === this.playerId) {
                    //         this.onCorrectionCallbacks.forEach((callback) =>
                    //             callback(message.position)
                    //         );
                    //     }
                    //     break;

                    case "playerAttack":
                        this.onPlayerAttackCallbacks.forEach((callback) =>
                            callback(message.playerId, message.position)
                        );
                        break;
                }
            }
        } catch {
            // Handle any errors in message processing
        }
    }

    // Public methods to register callbacks
    public onPlayerJoined(callback: OnPlayerJoinedCallback): void {
        this.onPlayerJoinedCallbacks.push(callback);
    }

    public onPlayerLeft(callback: OnPlayerLeftCallback): void {
        this.onPlayerLeftCallbacks.push(callback);
    }

    public onPlayerMovement(callback: OnPlayerMovementCallback): void {
        this.onPlayerMovementCallbacks.push(callback);
    }

    public onPlayerDirection(callback: OnPlayerDirectionCallback): void {
        this.onPlayerDirectionCallbacks.push(callback);
    }

    public onGameState(callback: OnGameStateCallback): void {
        this.onGameStateCallbacks.push(callback);
    }

    public onCorrection(callback: OnCorrectionCallback): void {
        this.onCorrectionCallbacks.push(callback);
    }

    public onMovementAck(callback: OnMovementAckCallback): void {
        this.onMovementAckCallbacks.push(callback);
    }

    public onLatency(callback: OnLatencyCallback): void {
        this.onLatencyCallbacks.push(callback);
    }

    public onPlayerAttack(callback: OnPlayerAttackCallback): void {
        this.onPlayerAttackCallbacks.push(callback);
    }

    // Send movement to server
    public sendMovement(dx: number, dy: number, inputSequence: number): void {
        const moveMsg = {
            type: "move" as const,
            movementVector: { dx, dy },
            inputSequence,
        };

        // Use binary protocol for frequent updates
        const binaryData = BinaryProtocol.encodeMove(moveMsg);

        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }
    }

    // Send direction change to server
    public sendDirection(direction: -1 | 1): void {
        const dirMsg = {
            type: "direction" as const,
            direction,
        };

        // Use binary protocol for frequent updates
        const binaryData = BinaryProtocol.encodeDirection(dirMsg);

        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }
    }

    // Send attack to server
    public sendAttack(binaryData: Uint8Array): void {
        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }
    }

    // Send attack end to server
    public sendAttackEnd(): void {
        const binaryData = BinaryProtocol.encodeAttackEnd();

        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }
    }

    private requestFullState(): void {
        if (!this.connected || this.resyncRequested) return;
        this.resyncRequested = true;
        const binaryData = BinaryProtocol.encodeSyncRequest();
        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }

        // Retry once if no full snapshot arrives. The flag is cleared by the
        // gameState branch, so a delivered snapshot cancels this path.
        if (this.resyncRetryTimer !== null) {
            window.clearTimeout(this.resyncRetryTimer);
        }
        this.resyncRetryTimer = window.setTimeout(() => {
            this.resyncRetryTimer = null;
            this.resyncRequested = false;
            this.requestFullState();
        }, 1000);
    }

    public onSessionStart(callback: OnSessionStartCallback): void {
        this.onSessionStartCallbacks.push(callback);
    }

    public hasProtocolMismatch(): boolean {
        return this.protocolMismatch;
    }

    // Get player ID
    public getPlayerId(): string {
        return this.playerId;
    }

    // Get initial position
    public getInitialPosition(): PlayerPosition {
        return this.initialPosition;
    }

    // Get all players
    public getPlayers(): Record<string, PlayerState> {
        return this.players;
    }

    // Server's current time-dilation factor (100 = nominal tick rate).
    public getDilationPct(): number {
        return this.dilationPct;
    }

    // Get connection status
    public getConnectionStatus(): string {
        if (this.worker) {
            // For worker, we can't directly check socket state, assume connected if worker exists
            return 'Connected (Worker)';
        } else if (this.socket) {
            switch (this.socket.readyState) {
                case WebSocket.CONNECTING: return 'Connecting';
                case WebSocket.OPEN: return 'Connected';
                case WebSocket.CLOSING: return 'Closing';
                case WebSocket.CLOSED: return 'Disconnected';
                default: return 'Unknown';
            }
        }
        return 'Disconnected';
    }

    // Cleanup method
    public disconnect(): void {
        this.closedByClient = true;
        this.stopDirectPings();
        if (this.reconnectTimer !== null) {
            window.clearTimeout(this.reconnectTimer);
            this.reconnectTimer = null;
        }
        if (this.resyncRetryTimer !== null) {
            window.clearTimeout(this.resyncRetryTimer);
            this.resyncRetryTimer = null;
        }
        if (this.worker) {
            this.worker.postMessage({ type: 'disconnect' });
            this.worker.terminate();
            this.worker = null;
        } else if (this.socket) {
            this.socket.close();
            this.socket = null;
        }
    }

    private emitLatency(latencyMs: number): void {
        this.onLatencyCallbacks.forEach((callback) => callback(latencyMs));
    }

    private startDirectPings(): void {
        this.stopDirectPings();
        const send = () => {
            if (!this.socket || this.socket.readyState !== WebSocket.OPEN) return;
            this.directPingNonce = (this.directPingNonce + 1) >>> 0;
            this.directPendingPings.set(this.directPingNonce, performance.now());
            this.socket.send(BinaryProtocol.encodePing(this.directPingNonce) as Uint8Array<ArrayBuffer>);
        };
        send();
        this.directPingTimer = window.setInterval(send, 1000);
    }

    private stopDirectPings(): void {
        if (this.directPingTimer !== null) {
            window.clearInterval(this.directPingTimer);
            this.directPingTimer = null;
        }
        this.directPendingPings.clear();
    }
}
