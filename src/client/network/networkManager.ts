import { BinaryProtocol } from "./protocol/binaryProtocol";
import {
    PlayerState,
    PlayerPosition,
    PROTOCOL_VERSION,
    type PlayerAttributes
} from "./protocol/messages";
import { DEFAULT_UNIT_TYPE, isValidUnitType, type UnitType } from "../../shared/units";
import type { Direction } from "../utils/animationLayout";

const MAX_DEAD_RECKON_TICKS = 20;

export type OnPlayerJoinedCallback = (player: PlayerState) => void;
export type OnPlayerLeftCallback = (playerId: string) => void;
export type OnPlayerMovementCallback = (
    playerId: string,
    dx: number,
    dy: number
) => void;
export type OnPlayerDirectionCallback = (
    playerId: string,
    direction: Direction
) => void;
export type OnGameStateCallback = (
    players: Record<string, PlayerState>,
    stateSequence: number | undefined,
    fullState: boolean,

    elapsedTicks: number,

    dilationPct: number
) => void;
export type OnCorrectionCallback = (position: PlayerPosition) => void;
export type OnMovementAckCallback = (position: PlayerPosition, inputSequence: number) => void;
export type OnSessionStartCallback = (position: PlayerPosition) => void;
export type OnLatencyCallback = (latencyMs: number) => void;
export type OnPlayerAttackCallback = (
    playerId: string,
    position: PlayerPosition,
    comboStep: number
) => void;

export type OnUnitRosterCallback = (entries: Record<string, PlayerAttributes>) => void;

export class NetworkManager {
    private socket: WebSocket | null = null;
    private worker: Worker | null = null;
    private useWorker: boolean = true;
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

    private dilationPct = 100;

    private requestedUnitType: UnitType;

    private myUnitType = 0;

    private playerAttributes: Record<string, PlayerAttributes> = {};

    private wsUrl = "";
    private reconnectTimer: number | null = null;
    private reconnectAttempts = 0;
    private closedByClient = false;
    private protocolMismatch = false;
    private directPingTimer: number | null = null;
    private directPingNonce = 0;
    private directPendingPings = new Map<number, number>();

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
    private onUnitRosterCallbacks: OnUnitRosterCallback[] = [];

    constructor() {

        this.requestedUnitType = DEFAULT_UNIT_TYPE;
    }

    /**
     * Connects with the given unit type. Safe to call again on an already-
     * connected instance (respawn): tears down the previous connection and
     * resets all per-session state first, so the new connection starts clean
     * regardless of how the old one was doing.
     */
    public connect(unitType: UnitType): void {
        this.requestedUnitType = isValidUnitType(unitType) ? unitType : DEFAULT_UNIT_TYPE;

        this.teardownConnection();
        this.resetSessionState();
        this.closedByClient = false;
        this.reconnectAttempts = 0;

        if (this.useWorker && typeof Worker !== 'undefined') {
            this.initWorker();
        } else {
            this.initDirectSocket();
        }
    }

    private teardownConnection(): void {
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
        }
        if (this.socket) {
            this.socket.close();
            this.socket = null;
        }
        this.connected = false;
    }

    private resetSessionState(): void {
        this.playerId = "";
        this.players = {};
        this.lastStateSequence = 0;
        this.hasStateSequence = false;
        this.resyncRequested = false;
        this.receivedInitialPosition = false;
        this.lastWorldTick = 0;
        this.hasWorldTick = false;
        this.dilationPct = 100;
        this.myUnitType = 0;
        this.playerAttributes = {};
        this.protocolMismatch = false;
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
        return `${protocol}//${window.location.host}/ws?unit=${this.requestedUnitType}`;
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

        this.playerId = "";
        this.players = {};
        this.lastStateSequence = 0;
        this.hasStateSequence = false;
        this.receivedInitialPosition = false;
        this.lastWorldTick = 0;
        this.hasWorldTick = false;
        this.myUnitType = 0;
        this.playerAttributes = {};

        this.scheduleReconnect();
    }

    private scheduleReconnect() {
        if (this.closedByClient || this.reconnectTimer !== null) return;

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

        console.error('WebSocket error');
    }

    private isNewerStateSequence(next: number, current: number): boolean {
        if (current === 0) return true;
        if (next === current) return false;

        const delta = (next - current) >>> 0;
        return delta < 0x80000000;
    }

    private setupSocketEvents() {
        if (!this.socket) return;

        this.socket.addEventListener("open", () => this.onSocketOpen());

        this.socket.addEventListener("message", async (event) => {
            try {
                let processedData = event.data;

                if (processedData instanceof Blob) {
                    processedData = await processedData.arrayBuffer();
                }

                this.handleServerMessage(processedData);
            } catch (error) {
                console.error("Error processing server message:", error);
            }
        });

        this.socket.addEventListener("close", () => this.onSocketClose());

        this.socket.addEventListener("error", () => {
            console.error('WebSocket error');
        });
    }

    private handleServerMessage(data: string | ArrayBuffer) {
        try {

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
                        this.myUnitType = message.unitType ?? 0;
                        break;

                    case "unitRoster":
                        Object.assign(this.playerAttributes, message.entries);
                        this.onUnitRosterCallbacks.forEach((callback) =>
                            callback(message.entries)
                        );
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

                        if (!this.playerId && message.players) {
                            const playerIds = Object.keys(message.players);
                            if (playerIds.length > 0) {
                                this.playerId = playerIds[playerIds.length - 1];

                                if (message.players[this.playerId]) {
                                    this.initialPosition = message.players[this.playerId].position;
                                }
                            }
                        }

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

                            this.onSessionStartCallbacks.forEach((callback) =>
                                callback(this.initialPosition)
                            );
                        }

                        Object.entries(incomingPlayers).forEach(([id, player]) => {
                            const isLocalPlayer = id === this.playerId;
                            const prev = prevPlayers[id];

                            if (!isLocalPlayer && player.moving !== prev?.moving) {
                                this.onPlayerMovementCallbacks.forEach((cb) =>
                                    cb(id, player.vx ?? 0, player.vy ?? 0)
                                );
                            }

                            const comboAdvanced = player.attacking && prev?.attacking &&
                                player.comboStep !== prev?.comboStep;
                            if (player.attacking && (!prev?.attacking || comboAdvanced)) {
                                this.onPlayerAttackCallbacks.forEach((cb) =>
                                    cb(id, player.position, player.comboStep ?? 1)
                                );
                            }
                        });

                        if (fullState) {
                            this.players = incomingPlayers;
                        } else {

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

                    case "playerAttack":

                        this.onPlayerAttackCallbacks.forEach((callback) =>
                            callback(message.playerId, message.position, 1)
                        );
                        break;
                }
            }
        } catch {

        }
    }

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

    public onUnitRoster(callback: OnUnitRosterCallback): void {
        this.onUnitRosterCallbacks.push(callback);
    }

    public sendMovement(dx: number, dy: number, inputSequence: number, sprint: boolean): void {
        const moveMsg = {
            type: "move" as const,
            movementVector: { dx, dy },
            inputSequence,
            sprint,
        };

        const binaryData = BinaryProtocol.encodeMove(moveMsg);

        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }
    }

    public sendDirection(direction: Direction): void {
        const dirMsg = {
            type: "direction" as const,
            direction,
        };

        const binaryData = BinaryProtocol.encodeDirection(dirMsg);

        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }
    }

    public sendAttack(binaryData: Uint8Array): void {
        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }
    }

    public sendAttackEnd(): void {
        const binaryData = BinaryProtocol.encodeAttackEnd();

        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }
    }

    public sendBlockStart(): void {
        const binaryData = BinaryProtocol.encodeBlockStart();

        if (this.worker) {
            this.worker.postMessage({ type: 'send', data: binaryData });
        } else if (this.socket && this.socket.readyState === WebSocket.OPEN) {
            this.socket.send(binaryData as Uint8Array<ArrayBuffer>);
        }
    }

    public sendBlockEnd(): void {
        const binaryData = BinaryProtocol.encodeBlockEnd();

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

    public getPlayerId(): string {
        return this.playerId;
    }

    public getInitialPosition(): PlayerPosition {
        return this.initialPosition;
    }

    public getPlayers(): Record<string, PlayerState> {
        return this.players;
    }

    public getMyUnitType(): number {
        return this.myUnitType;
    }

    public getRequestedUnitType(): UnitType {
        return this.requestedUnitType;
    }

    public getUnitType(playerId?: string): number {
        if (playerId === undefined || playerId === this.playerId) return this.myUnitType;
        return this.playerAttributes[playerId]?.unitType ?? 0;
    }

    public getHp(playerId?: string): number | undefined {
        return this.playerAttributes[playerId ?? this.playerId]?.hp;
    }

    public getStamina(playerId?: string): number | undefined {
        return this.playerAttributes[playerId ?? this.playerId]?.stamina;
    }

    public getDilationPct(): number {
        return this.dilationPct;
    }

    public getConnectionStatus(): string {
        if (this.worker) {

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

    public disconnect(): void {
        this.teardownConnection();
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
