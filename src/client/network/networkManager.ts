import { BinaryProtocol } from "./protocol/binaryProtocol";
import {
    PlayerState,
    PlayerPosition
} from "./protocol/messages";

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
    players: Record<string, PlayerState>
) => void;
export type OnCorrectionCallback = (position: PlayerPosition) => void;
export type OnMovementAckCallback = (position: PlayerPosition, inputSequence: number) => void;
export type OnPlayerAttackCallback = (
    playerId: string,
    position: PlayerPosition
) => void;

export class NetworkManager {
    private socket: WebSocket | null = null;
    private worker: Worker | null = null;
    private useWorker: boolean = true; // Use Web Worker for WebSocket to avoid blocking main thread
    private playerId: string = "";
    private initialPosition: PlayerPosition = { x: 0, y: 0 };
    private players: Record<string, PlayerState> = {};

    // Callback handlers
    private onPlayerJoinedCallbacks: OnPlayerJoinedCallback[] = [];
    private onPlayerLeftCallbacks: OnPlayerLeftCallback[] = [];
    private onPlayerMovementCallbacks: OnPlayerMovementCallback[] = [];
    private onPlayerDirectionCallbacks: OnPlayerDirectionCallback[] = [];
    private onGameStateCallbacks: OnGameStateCallback[] = [];
    private onCorrectionCallbacks: OnCorrectionCallback[] = [];
    private onMovementAckCallbacks: OnMovementAckCallback[] = [];
    private onPlayerAttackCallbacks: OnPlayerAttackCallback[] = [];

    // Reference to FPS display for ping tracking
    private fpsDisplay: any = null;

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
                }
            };

            this.worker.onerror = (error) => {
                console.error('Network Worker error:', error);
                // Fallback to direct socket
                this.useWorker = false;
                this.initDirectSocket();
            };

            const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
            const wsUrl = `${protocol}//${window.location.host}/ws`;

            this.worker.postMessage({ type: 'connect', url: wsUrl });
        } catch (error) {
            console.warn('Failed to initialize Web Worker, falling back to direct WebSocket:', error);
            this.useWorker = false;
            this.initDirectSocket();
        }
    }

    private initDirectSocket() {
        const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
        const wsUrl = `${protocol}//${window.location.host}/ws`;

        this.socket = new WebSocket(wsUrl);
        this.setupSocketEvents();
    }

    private onSocketOpen() {}

    private onSocketClose() {}

    private onSocketError() {
        // Handle connection error
        console.error('WebSocket error');
    }

    private setupSocketEvents() {
        if (!this.socket) return;

        // Connection established
        this.socket.addEventListener("open", () => {});

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
        this.socket.addEventListener("close", () => {});

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
                    case "deltaGameState":

                        // If we don't have a player ID yet, determine it from the game state
                        if (!this.playerId && message.players) {
                            const playerIds = Object.keys(message.players);
                            if (playerIds.length > 0) {
                                this.playerId = playerIds[playerIds.length - 1];

                                if (message.players[this.playerId]) {
                                    this.initialPosition = message.players[this.playerId].position;
                                }
                            }
                        }

                        const prevPlayers = this.players;

                        if (message.type === "deltaGameState") {
                            // Delta: merge changed players into existing state
                            this.players = { ...this.players };
                            for (const [id, player] of Object.entries(message.players as Record<string, PlayerState>)) {
                                this.players[id] = player;
                            }
                        } else {
                            // Full state: replace entirely
                            this.players = message.players;
                        }

                        // Fire animation callbacks based on state changes
                        Object.entries(message.players as Record<string, PlayerState>).forEach(([id, player]) => {
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

                        this.onGameStateCallbacks.forEach((callback) =>
                            callback(this.players)
                        );
                        break;

                    case "movementAck":

                        if (message.playerId === this.playerId) {
                            this.onMovementAckCallbacks.forEach((callback) =>
                                callback(message.position, message.inputSequence)
                            );
                        }
                        break;

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
        } catch (error) {
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

    public onPlayerAttack(callback: OnPlayerAttackCallback): void {
        this.onPlayerAttackCallbacks.push(callback);
    }

    // Send movement to server
    public sendMovement(dx: number, dy: number, inputSequence?: number): void {
        const moveMsg = {
            type: "move" as const,
            movementVector: { dx, dy },
            inputSequence: inputSequence || 0,
            position: { x: 0, y: 0 },
        };

        // Track ping if FPS display is available
        if (this.fpsDisplay && inputSequence !== undefined) {
            this.fpsDisplay.trackMovementSend(inputSequence);
        }

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
        if (this.worker) {
            this.worker.postMessage({ type: 'disconnect' });
            this.worker.terminate();
            this.worker = null;
        } else if (this.socket) {
            this.socket.close();
            this.socket = null;
        }
    }

    // Set FPS display reference for ping tracking
    public setFpsDisplay(fpsDisplay: any): void {
        this.fpsDisplay = fpsDisplay;
    }
}
