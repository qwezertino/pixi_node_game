import { BinaryProtocol } from "../../protocol/binaryProtocol";
import { PlayerState, PlayerPosition } from "../../protocol/messages";

// Callback types
export type OnPlayerJoinedCallback = (player: PlayerState) => void;
export type OnPlayerLeftCallback = (playerId: string) => void;
export type OnPlayerMovementCallback = (playerId: string, dx: number, dy: number) => void;
export type OnPlayerDirectionCallback = (playerId: string, direction: -1 | 1) => void;
export type OnGameStateCallback = (players: Record<string, PlayerState>) => void;
export type OnCorrectionCallback = (position: PlayerPosition) => void;

export class NetworkManager {
    private socket: WebSocket;
    private playerId: string = '';
    private initialPosition: PlayerPosition = { x: 0, y: 0 };
    private players: Record<string, PlayerState> = {};

    // Callback handlers
    private onPlayerJoinedCallbacks: OnPlayerJoinedCallback[] = [];
    private onPlayerLeftCallbacks: OnPlayerLeftCallback[] = [];
    private onPlayerMovementCallbacks: OnPlayerMovementCallback[] = [];
    private onPlayerDirectionCallbacks: OnPlayerDirectionCallback[] = [];
    private onGameStateCallbacks: OnGameStateCallback[] = [];
    private onCorrectionCallbacks: OnCorrectionCallback[] = [];

    constructor() {
        const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
        const host = 'localhost:8108'; // Change to window.location.host in production
        const wsUrl = `${protocol}//${host}/ws`;

        this.socket = new WebSocket(wsUrl);
        this.setupSocketEvents();
    }

    private setupSocketEvents() {
        // Connection established
        this.socket.addEventListener("open", () => {
            console.log("Connected to game server");
            // We'll automatically receive initial state from server
        });

        // Receive messages from server
        this.socket.addEventListener("message", async (event) => {
            try {
                let processedData = event.data;

                // Handle Blob data
                if (processedData instanceof Blob) {
                    console.log("Received blob data, size:", processedData.size);
                    // Convert Blob to ArrayBuffer
                    processedData = await processedData.arrayBuffer();
                }

                this.handleServerMessage(processedData);
            } catch (error) {
                console.error("Error processing WebSocket message:", error);
            }
        });

        // Connection closed
        this.socket.addEventListener("close", () => {
            console.log("Disconnected from game server");
        });

        // Connection error
        this.socket.addEventListener("error", (error) => {
            console.error("WebSocket error:", error);
        });
    }

    private handleServerMessage(data: string | ArrayBuffer) {
        try {
            // Handle binary message
            if (data instanceof ArrayBuffer) {
                console.log("Processing ArrayBuffer data, size:", data.byteLength);
                const message = BinaryProtocol.decodeMessage(new Uint8Array(data));

                if (!message) {
                    console.warn("Failed to decode binary message");
                    return;
                }

                console.log("Decoded message type:", message.type);

                switch (message.type) {
                    case 'playerMovement':
                        this.onPlayerMovementCallbacks.forEach(callback =>
                            callback(message.playerId, message.movementVector.dx, message.movementVector.dy)
                        );
                        break;

                    case 'playerDirection':
                        this.onPlayerDirectionCallbacks.forEach(callback =>
                            callback(message.playerId, message.direction)
                        );
                        break;

                    case 'gameState':
                        this.players = message.players;
                        this.onGameStateCallbacks.forEach(callback => callback(message.players));
                        break;

                    case 'correction':
                        if (message.playerId === this.playerId) {
                            this.onCorrectionCallbacks.forEach(callback => callback(message.position));
                        }
                        break;
                }
            }
            // Handle JSON message
            else {
                // Добавляем проверку, является ли строка валидным JSON
                // и логирование для отладки
                if (typeof data !== 'string') {
                    console.warn("Received non-string data:", data);
                    return;
                }

                // Выводим полученные данные для отладки
                console.debug("Received string data:", data.substring(0, 100) + (data.length > 100 ? "...": ""));

                // Проверка на пустые сообщения или сообщения-пинги
                if (!data || data === 'ping') {
                    console.debug("Ignoring empty or ping message");
                    return;
                }

                try {
                    const message = JSON.parse(data);

                    if (!message.type) {
                        console.warn("Received message without type:", message);
                        return;
                    }

                    switch (message.type) {
                        case 'initialState':
                            this.playerId = message.player.id;
                            this.initialPosition = message.player.position;
                            this.players = message.players;

                            // Notify about initial game state
                            this.onGameStateCallbacks.forEach(callback => callback(message.players));
                            break;

                        case 'playerJoined':
                            this.players[message.player.id] = message.player;
                            this.onPlayerJoinedCallbacks.forEach(callback => callback(message.player));
                            break;

                        case 'playerLeft':
                            delete this.players[message.playerId];
                            this.onPlayerLeftCallbacks.forEach(callback => callback(message.playerId));
                            break;

                        default:
                            console.debug("Unknown message type:", message.type);
                            break;
                    }
                } catch (parseError) {
                    console.error("Failed to parse JSON:", parseError);
                    console.error("Raw data causing the error:", data);
                }
            }
        } catch (error) {
            console.error("Error processing server message:", error);
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

    // Send movement to server
    public sendMovement(dx: number, dy: number): void {
        if (this.socket.readyState !== WebSocket.OPEN) return;

        const moveMsg = {
            type: 'move' as const,
            movementVector: { dx, dy }
        };

        // Use binary protocol for frequent updates
        const binaryData = BinaryProtocol.encodeMove(moveMsg);
        this.socket.send(binaryData);
    }

    // Send direction change to server
    public sendDirection(direction: -1 | 1): void {
        if (this.socket.readyState !== WebSocket.OPEN) return;

        const dirMsg = {
            type: 'direction' as const,
            direction
        };

        // Use binary protocol for frequent updates
        const binaryData = BinaryProtocol.encodeDirection(dirMsg);
        this.socket.send(binaryData);
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
}