// Web Worker for handling WebSocket to avoid blocking main thread

interface WorkerMessage {
    type: 'connect' | 'send' | 'disconnect';
    url?: string;
    data?: any;
}

interface SocketMessage {
    type: 'message' | 'open' | 'close' | 'error' | 'latency';
    data?: any;
    event?: any;
    latencyMs?: number;
}

let socket: WebSocket | null = null;
let pingTimer: number | null = null;
let pingNonce = 0;
const pendingPings = new Map<number, number>();

const PING = 17;
const PONG = 18;

self.onmessage = (e: MessageEvent<WorkerMessage>) => {
    const msg = e.data;

    switch (msg.type) {
        case 'connect':
            if (msg.url) {
                connect(msg.url);
            }
            break;
        case 'send':
            if (socket && socket.readyState === WebSocket.OPEN && msg.data) {
                socket.send(msg.data);
            }
            break;
        case 'disconnect':
            stopPings();
            if (socket) {
                socket.close();
                socket = null;
            }
            break;
    }
};

function connect(url: string) {
    // A reconnect must not leave the previous socket delivering events: its
    // handlers would keep posting messages for a session the main thread has
    // already reset.
    if (socket) {
        socket.onopen = null;
        socket.onmessage = null;
        socket.onclose = null;
        socket.onerror = null;
        socket.close();
        socket = null;
    }
    stopPings();

    socket = new WebSocket(url);
    socket.binaryType = 'arraybuffer';

    socket.onopen = () => {
        postMessage({ type: 'open' });
        sendPing();
        pingTimer = self.setInterval(sendPing, 1000);
    };

    socket.onmessage = async (event) => {
        let data = event.data;

        // Handle Blob data (convert to ArrayBuffer for consistency)
        if (data instanceof Blob) {
            data = await data.arrayBuffer();
        }

        if (data instanceof ArrayBuffer && data.byteLength === 5) {
            const view = new DataView(data);
            if (view.getUint8(0) === PONG) {
                const nonce = view.getUint32(1, true);
                const sentAt = pendingPings.get(nonce);
                if (sentAt !== undefined) {
                    pendingPings.delete(nonce);
                    postMessage({ type: 'latency', latencyMs: performance.now() - sentAt });
                }
                return;
            }
        }

        postMessage({ type: 'message', data }, data instanceof ArrayBuffer ? [data] : []);
    };

    socket.onclose = () => {
        stopPings();
        postMessage({ type: 'close' });
    };

    socket.onerror = (error) => {
        postMessage({ type: 'error', event: error });
    };
}

function sendPing() {
    if (!socket || socket.readyState !== WebSocket.OPEN) return;
    pingNonce = (pingNonce + 1) >>> 0;
    const buffer = new ArrayBuffer(5);
    const view = new DataView(buffer);
    view.setUint8(0, PING);
    view.setUint32(1, pingNonce, true);
    pendingPings.set(pingNonce, performance.now());
    socket.send(buffer);

    if (pendingPings.size > 8) {
        const oldest = pendingPings.keys().next().value;
        if (oldest !== undefined) pendingPings.delete(oldest);
    }
}

function stopPings() {
    if (pingTimer !== null) {
        self.clearInterval(pingTimer);
        pingTimer = null;
    }
    pendingPings.clear();
}

function postMessage(msg: SocketMessage, transfer: Transferable[] = []) {
    const workerScope = self as unknown as {
        postMessage(message: SocketMessage, transfer: Transferable[]): void;
    };
    workerScope.postMessage(msg, transfer);
}
