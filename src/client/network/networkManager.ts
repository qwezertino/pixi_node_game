import { BinaryProtocol } from "../../protocol/binaryProtocol";
import {
    PlayerState,
    PlayerPosition
} from "../../protocol/messages";

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
    private socket: WebSocket;
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
        const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
        const host = "localhost:8108"; // Change to window.location.host in production
        const wsUrl = `${protocol}//${host}/ws`;

        this.socket = new WebSocket(wsUrl);
        this.setupSocketEvents();
    }

    private setupSocketEvents() {
        // Connection established
        this.socket.addEventListener("open", () => {

            // We'll automatically receive initial state from server
        });

        // Receive messages from server
        this.socket.addEventListener("message", async (event) => {
            try {
                let processedData = event.data;

                // Handle Blob data
                if (processedData instanceof Blob) {
                    // Convert Blob to ArrayBuffer
                    processedData = await processedData.arrayBuffer();
                }

                this.handleServerMessage(processedData);
            } catch (error) {
                console.error("Error processing server message:", error);
            }
        });

        // Connection closed
        this.socket.addEventListener("close", () => {

        });

        // Connection error
        this.socket.addEventListener("error", () => {
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
                        } else {
                            console.log("⏭️  Skipping own movement or invalid data");
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

                        // If we don't have a player ID yet, determine it from the game state
                        if (!this.playerId && message.players) {
                            const playerIds = Object.keys(message.players);
                            if (playerIds.length > 0) {
                                // For now, assume we're the first player in the list
                                // This is a simplified approach - in real game this should be handled differently
                                this.playerId = playerIds[playerIds.length - 1]; // Take the last player (most recently joined)

                                if (message.players[this.playerId]) {
                                    this.initialPosition = message.players[this.playerId].position;
                                }
                            }
                        }

                        this.players = message.players;
                        this.onGameStateCallbacks.forEach((callback) =>
                            callback(message.players)
                        );
                        break;

                    case "movementAck":

                        if (message.playerId === this.playerId) {
                            this.onMovementAckCallbacks.forEach((callback) =>
                                callback(message.position, message.inputSequence)
                            );
                        } else {
                            console.log("⏭️  Skipping other player's movement ack");
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
    public sendMovement(dx: number, dy: number, inputSequence?: number, position?: { x: number; y: number }): void {
        if (this.socket.readyState !== WebSocket.OPEN) return;

        const moveMsg = {
            type: "move" as const,
            movementVector: { dx, dy },
            inputSequence: inputSequence || 0,
            position: position || { x: 0, y: 0 }, // Default position if not provided
        };

        // Track ping if FPS display is available
        if (this.fpsDisplay && inputSequence !== undefined) {
            this.fpsDisplay.trackMovementSend(inputSequence);
        }

        // Use binary protocol for frequent updates
        const binaryData = BinaryProtocol.encodeMove(moveMsg);
        this.socket.send(binaryData);
    }

    // Send direction change to server
    public sendDirection(direction: -1 | 1): void {
        if (this.socket.readyState !== WebSocket.OPEN) return;

        const dirMsg = {
            type: "direction" as const,
            direction,
        };

        // Use binary protocol for frequent updates
        const binaryData = BinaryProtocol.encodeDirection(dirMsg);
        this.socket.send(binaryData);
    }

    // Send attack to server
    public sendAttack(binaryData: Uint8Array): void {
        if (this.socket.readyState !== WebSocket.OPEN) return;
        this.socket.send(binaryData);
    }

    // Send attack end to server
    public sendAttackEnd(): void {
        if (this.socket.readyState !== WebSocket.OPEN) return;

        const binaryData = BinaryProtocol.encodeAttackEnd();
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

    // Set FPS display reference for ping tracking
    public setFpsDisplay(fpsDisplay: any) {
        this.fpsDisplay = fpsDisplay;
    }
}
