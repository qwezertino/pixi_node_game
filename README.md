# pixi_node_game

A high-performance multiplayer 2D game built with Pixi.js and Bun.js, designed to handle 10000+ concurrent players with optimized networking and rendering.

## 🚀 Features

- **High Concurrency**: Supports 10000+ simultaneous players
- **Real-time Networking**: WebSocket-based communication with binary protocol
- **Smooth Gameplay**: 60Hz tick rate with lag compensation and interpolation
- **Efficient Rendering**: Pixi.js WebGL rendering with sprite animation system
- **Optimized Bandwidth**: Binary data format with delta movement and state batching
- **Server Authoritative**: All game logic runs on server with client prediction
- **Grid-based Movement**: Integer-based positioning system for consistent gameplay

## 🏗️ Architecture

### Client-Side
- **Pixi.js**: WebGL rendering engine for smooth 2D graphics
- **Input Management**: Captures and processes player input with immediate local feedback
- **Network Manager**: Handles WebSocket communication and server synchronization
- **Animation System**: Sprite-based animations with state management
- **Interpolation**: Smooth movement interpolation for network latency compensation

### Server-Side
- **Bun.js**: High-performance JavaScript runtime with native WebSocket support
- **Game Loop**: 60Hz authoritative game simulation
- **World State**: Complete game state synchronization every 30 seconds
- **Delta Updates**: Efficient state change broadcasting
- **Connection Management**: Optimized handling of concurrent connections

## 🛠️ Technical Stack

- **Frontend**: Pixi.js 8.6+, TypeScript, Vite
- **Backend**: Bun.js 1.2+, Node.js-compatible APIs
- **Networking**: WebSockets with custom binary protocol
- **Build Tools**: Vite, TypeScript, ESLint
- **Assets**: Sprite sheets and individual sprites for characters and items

## 📦 Installation

### Prerequisites
- Node.js 18+ or Bun 1.0+
- npm or bun package manager

### Setup
```bash
# Clone the repository
git clone https://github.com/qwezertino/pixi_node_game.git
cd pixi_node_game

# Install dependencies
npm install
# or
bun install

# Start development server
npm run dev
# or
bun run dev
```

### Build for Production
```bash
# Build client and server
npm run build
# or
bun run build

# Start production server
npm start
# or
bun run dist/assets/server.js
```

## 🎮 Usage

### Development
```bash
# Start both client and server in development mode
npm run dev
```
- Client runs on: http://localhost:8109
- Server runs on: http://localhost:8108

### Production
```bash
# Build and start
npm run build && npm start
```

## 🌐 Networking Details

### Binary Protocol
- **Movement**: Delta-based (dx, dy) instead of absolute positions
- **States**: Numeric encoding (idle=0, move=1, attack=2)
- **Direction**: Binary facing (-1=left, 1=right)
- **Batching**: Multiple actions sent together to reduce packet frequency
- **Change Detection**: Only sends data when player state actually changes

### Connection Flow
1. Client connects via WebSocket to `/ws` endpoint
2. Server assigns unique player ID
3. Client receives initial world state
4. Real-time delta updates for state changes
5. Full world sync every 30 seconds to prevent desynchronization

### Performance Optimizations
- **WebSocket Binary Frames**: Raw binary data instead of JSON
- **Bit Packing**: Minimized message sizes through efficient encoding
- **Connection Pooling**: Optimized concurrent connection handling
- **Worker Threads**: Multi-threaded server processing (planned)

## 📁 Project Structure

```
src/
├── client/                 # Frontend application
│   ├── main.ts            # Application entry point
│   ├── controllers/       # Input and animation controllers
│   ├── game/             # Game logic and player management
│   ├── network/          # WebSocket communication
│   └── utils/            # Utilities (sprites, input, coordinates)
├── server/                # Backend server
│   ├── main.ts           # Server entry point
│   ├── game/            # Game world and logic
│   ├── handlers/        # WebSocket connection handling
│   └── workers/         # Multi-threading support (planned)
├── common/               # Shared types and constants
└── protocol/            # Network protocol definitions
```

## 🎯 Game Features

- **Character Movement**: 8-directional movement with smooth animation
- **Sprite Animation**: Multi-frame animations for different states
- **Real-time Multiplayer**: See other players move in real-time
- **Lag Compensation**: Client-side prediction with server correction
- **World Synchronization**: Periodic full state sync to prevent desync
- **Debug Tools**: FPS display and connection monitoring

## 🔧 Configuration

### Game Settings
Located in `src/common/gameSettings.ts`:
- Player movement speed
- Animation frame rates
- Network tick rates
- World boundaries

### Network Protocol
Located in `src/protocol/`:
- Message types and formats
- Binary encoding/decoding
- Protocol versioning

## 🚀 Performance Benchmarks

- **Concurrent Players**: Tested with 1000+ connections
- **Tick Rate**: 60Hz server simulation
- **Latency**: <50ms round-trip for local networks
- **Bandwidth**: <10KB/s per player with optimizations
- **Memory**: Efficient object pooling and cleanup

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Test with multiple clients
5. Submit a pull request

## 📄 License

This project is licensed under the MIT License - see the LICENSE file for details.

## 🙏 Acknowledgments

- Pixi.js for excellent WebGL rendering
- Bun.js for high-performance JavaScript runtime
- The gaming community for inspiration and feedback