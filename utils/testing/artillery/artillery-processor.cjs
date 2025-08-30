// Binary protocol constants and helpers
const MessageType = {
  JOIN: 1,
  LEAVE: 2,
  MOVE: 3,
  DIRECTION: 4,
  ATTACK: 5,
  ATTACK_END: 6,
  GAME_STATE: 7,
  MOVEMENT_ACK: 8,
  CORRECTION: 9,
  INITIAL_STATE: 10,
  PLAYER_JOINED: 11,
  PLAYER_LEFT: 12,
};

// Binary encoding helpers
function packMovement(dx, dy) {
  let packed = 0;
  packed |= (dx + 1) & 0x03; // dx: -1->0, 0->1, 1->2 (2 bits)
  packed |= ((dy + 1) & 0x03) << 2; // dy: same, shifted 2 bits
  return packed;
}

// Encode binary move message with position (compatible with current server)
function encodeMove(movementVector, inputSequence, position = { x: 400, y: 300 }) {
  const buffer = new ArrayBuffer(14); // Updated to 14 bytes to include position
  const view = new DataView(buffer);
  view.setUint8(0, MessageType.MOVE);

  const dx = Math.sign(movementVector.dx) || 0;
  const dy = Math.sign(movementVector.dy) || 0;
  const packed = packMovement(dx, dy);

  view.setUint8(1, packed);
  view.setUint32(2, inputSequence, true);

  // Add position data (x, y as uint32)
  view.setUint32(6, Math.floor(position.x), true);
  view.setUint32(10, Math.floor(position.y), true);

  return new Uint8Array(buffer);
}

// Encode binary direction message
function encodeDirection(direction) {
  const buffer = new ArrayBuffer(2);
  const view = new DataView(buffer);
  view.setUint8(0, MessageType.DIRECTION);
  view.setInt8(1, direction);
  return new Uint8Array(buffer);
}

// Encode binary attack message
function encodeAttack(position) {
  const buffer = new ArrayBuffer(9);
  const view = new DataView(buffer);
  view.setUint8(0, MessageType.ATTACK);
  view.setFloat32(1, position.x, true);
  view.setFloat32(5, position.y, true);
  return new Uint8Array(buffer);
}

// Encode binary attack end message
function encodeAttackEnd() {
  const buffer = new ArrayBuffer(1);
  const view = new DataView(buffer);
  view.setUint8(0, MessageType.ATTACK_END);
  return new Uint8Array(buffer);
}

module.exports = {
  // Initialize client with proper state tracking
  initializeClient: function(context, events, done) {
    context.vars.inputSequence = 1;
    // Use spawn area from gameConfig (100-900 range)
    context.vars.position = {
      x: Math.floor(Math.random() * 800) + 100, // 100-900
      y: Math.floor(Math.random() * 800) + 100  // 100-900
    };
    context.vars.direction = 1;
    context.vars.attacking = false;
    context.vars.lastAttackTime = 0;
    context.vars.sessionStart = Date.now();
    context.vars.messagesSent = 0;
    context.vars.errors = 0;

    // Performance tracking
    context.vars.latencies = [];

    return done();
  },

  // Generate and send movement message directly
  generateAndSendMovement: function(context, events, done) {
    // Don't send movement if recently attacked
    let movement;
    if (context.vars.attacking && Date.now() - context.vars.lastAttackTime < 500) {
      movement = { dx: 0, dy: 0 };
    } else {
      // Generate realistic movement patterns
      const patterns = [
        { dx: 1, dy: 0 },   // Right
        { dx: -1, dy: 0 },  // Left
        { dx: 0, dy: 1 },   // Down
        { dx: 0, dy: -1 },  // Up
        { dx: 1, dy: 1 },   // Diagonal
        { dx: -1, dy: -1 }, // Diagonal
        { dx: 0, dy: 0 }    // Stop (realistic for games)
      ];
      movement = patterns[Math.floor(Math.random() * patterns.length)];
    }

    // Encode as binary and send directly with current position
    const binaryMessage = encodeMove(movement, context.vars.inputSequence++, context.vars.position);

    // Update position based on movement for next message
    if (movement.dx !== 0 || movement.dy !== 0) {
      context.vars.position.x += movement.dx * 4; // Using speed from gameConfig
      context.vars.position.y += movement.dy * 4;

      // Keep position within world bounds (from gameConfig)
      context.vars.position.x = Math.max(0, Math.min(2000, context.vars.position.x));
      context.vars.position.y = Math.max(0, Math.min(2000, context.vars.position.y));
    }

    if (context.ws && context.ws.readyState === 1) { // WebSocket.OPEN
      context.ws.send(binaryMessage);
      context.vars.messagesSent++;
    }

    return done();
  },

  // Maybe change direction and send directly
  maybeChangeAndSendDirection: function(context, events, done) {
    // Only change direction 15% of the time
    if (Math.random() > 0.15) {
      return done(); // Skip sending
    }

    context.vars.direction = context.vars.direction === 1 ? -1 : 1;

    // Encode as binary and send directly
    const binaryMessage = encodeDirection(context.vars.direction);

    if (context.ws && context.ws.readyState === 1) { // WebSocket.OPEN
      context.ws.send(binaryMessage);
      context.vars.messagesSent++;
    }

    return done();
  },

  // Maybe attack and send directly
  maybeAttackAndSend: function(context, events, done) {
    const now = Date.now();

    // Attack cooldown of 2 seconds minimum
    if (context.vars.attacking || (now - context.vars.lastAttackTime) < 2000) {
      return done(); // Skip sending
    }

    // Only attack 5% of the time when not on cooldown
    if (Math.random() > 0.05) {
      return done(); // Skip sending
    }

    context.vars.attacking = true;
    context.vars.lastAttackTime = now;

    // Generate random attack position near player
    const attackX = context.vars.position.x + (Math.random() - 0.5) * 100;
    const attackY = context.vars.position.y + (Math.random() - 0.5) * 100;

    // Encode as binary and send directly
    const binaryMessage = encodeAttack({
      x: Math.floor(attackX),
      y: Math.floor(attackY)
    });

    if (context.ws && context.ws.readyState === 1) { // WebSocket.OPEN
      context.ws.send(binaryMessage);
      context.vars.messagesSent++;
    }

    return done();
  },

  // Maybe end attack and send directly
  maybeAttackEndAndSend: function(context, events, done) {
    if (!context.vars.attacking) {
      return done(); // Skip sending
    }

    // End attack after 200-500ms
    const attackDuration = Date.now() - context.vars.lastAttackTime;
    if (attackDuration < 200) {
      return done(); // Skip sending
    }

    context.vars.attacking = false;

    // Encode as binary and send directly
    const binaryMessage = encodeAttackEnd();

    if (context.ws && context.ws.readyState === 1) { // WebSocket.OPEN
      context.ws.send(binaryMessage);
      context.vars.messagesSent++;
    }

    return done();
  },

  // Log performance metrics on disconnect
  logDisconnect: function(context, events, done) {
    const sessionDuration = Date.now() - context.vars.sessionStart;
    const messagesPerSecond = context.vars.messagesSent / (sessionDuration / 1000);

    events.emit('counter', 'game.session.completed', 1);
    events.emit('counter', 'game.session.total_messages', context.vars.messagesSent);
    events.emit('counter', 'game.session.errors', context.vars.errors);
    events.emit('rate', 'game.session.messages_per_second', messagesPerSecond);
    events.emit('histogram', 'game.session.duration_ms', sessionDuration);

    return done();
  },

  // Enhanced metrics tracking
  beforeScenario: function(context, events) {
    context.vars.scenarioStart = Date.now();
    context.vars.totalMoves = 0;
    context.vars.totalAttacks = 0;
    context.vars.totalDirections = 0;
    context.vars.connectionErrors = 0;
    context.vars.messageErrors = 0;
  },

  afterScenario: function(context, events) {
    const scenarioDuration = Date.now() - context.vars.scenarioStart;
    const durationSeconds = scenarioDuration / 1000;

    // Emit comprehensive metrics
    events.emit('counter', 'game.moves.total', context.vars.totalMoves);
    events.emit('counter', 'game.attacks.total', context.vars.totalAttacks);
    events.emit('counter', 'game.directions.total', context.vars.totalDirections);
    events.emit('counter', 'game.errors.connection', context.vars.connectionErrors);
    events.emit('counter', 'game.errors.message', context.vars.messageErrors);

    if (durationSeconds > 0) {
      events.emit('rate', 'game.actions.per_second',
        (context.vars.totalMoves + context.vars.totalAttacks + context.vars.totalDirections) / durationSeconds);
    }

    // Server performance indicators
    events.emit('histogram', 'game.scenario.duration_ms', scenarioDuration);
  },

  // Track message sending with error handling
  beforeRequest: function(requestParams, context, events) {
    if (requestParams.data) {
      try {
        const message = JSON.parse(requestParams.data);
        const now = Date.now();

        // Track message types
        switch(message.type) {
          case 'move':
            context.vars.totalMoves++;
            break;
          case 'attack':
            context.vars.totalAttacks++;
            break;
          case 'direction':
            context.vars.totalDirections++;
            break;
        }

        // Track message frequency for server load analysis
        if (context.vars.lastMessageTime) {
          const interval = now - context.vars.lastMessageTime;
          events.emit('histogram', 'game.message.interval_ms', interval);
        }
        context.vars.lastMessageTime = now;

        // Add latency tracking
        requestParams.startTime = now;

      } catch (error) {
        context.vars.messageErrors++;
        events.emit('counter', 'game.errors.parse', 1);
      }
    }
  },

  afterResponse: function(requestParams, response, context, events) {
    if (requestParams.startTime) {
      const latency = Date.now() - requestParams.startTime;
      events.emit('histogram', 'game.message.latency_ms', latency);

      // Track high latency events
      if (latency > 100) {
        events.emit('counter', 'game.latency.high', 1);
      }
    }
  },

  // WebSocket error handling
  onError: function(error, context, events) {
    context.vars.connectionErrors++;
    events.emit('counter', 'game.errors.websocket', 1);
    console.error('WebSocket error:', error.message);
  }
};
