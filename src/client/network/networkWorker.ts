// Web Worker for handling WebSocket to avoid blocking main thread

interface WorkerMessage {
    type: 'connect' | 'send' | 'disconnect';
    url?: string;
    data?: any;
}

interface SocketMessage {
    type: 'message' | 'open' | 'close' | 'error';
    data?: any;
    event?: any;
}

let socket: WebSocket | null = null;

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
            if (socket) {
                socket.close();
                socket = null;
            }
            break;
    }
};

function connect(url: string) {
    socket = new WebSocket(url);

    socket.onopen = () => {
        postMessage({ type: 'open' });
    };

    socket.onmessage = async (event) => {
        let data = event.data;

        // Handle Blob data (convert to ArrayBuffer for consistency)
        if (data instanceof Blob) {
            data = await data.arrayBuffer();
        }

        postMessage({ type: 'message', data });
    };

    socket.onclose = () => {
        postMessage({ type: 'close' });
    };

    socket.onerror = (error) => {
        postMessage({ type: 'error', event: error });
    };
}

function postMessage(msg: SocketMessage) {
    (self as any).postMessage(msg);
}
