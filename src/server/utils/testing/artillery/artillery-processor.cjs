module.exports = {
  // Initialize client with proper state tracking
  initializeClient: function(context, events, done) {
    context.vars.inputSequence = 1;
    context.vars.position = { x: 400, y: 300 }; // Start in center
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

  // Generate realistic movement with proper input sequence
  generateMovement: function(context, events, done) {
    // Don't send movement if recently attacked
    if (context.vars.attacking && Date.now() - context.vars.lastAttackTime < 500) {
      context.vars.movementMessage = JSON.stringify({
        type: "move",
        movementVector: { dx: 0, dy: 0 },
        inputSequence: context.vars.inputSequence++
      });
      return done();
    }

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

    const movement = patterns[Math.floor(Math.random() * patterns.length)];

    context.vars.movementMessage = JSON.stringify({
      type: "move",
      movementVector: movement,
      inputSequence: context.vars.inputSequence++,
      timestamp: Date.now()
    });

    context.vars.messagesSent++;
    return done();
  },

  // Change direction occasionally (not every frame)
  maybeChangeDirection: function(context, events, done) {
    // Only change direction 15% of the time
    if (Math.random() > 0.15) {
      context.vars.directionMessage = null;
      return done();
    }

    context.vars.direction = context.vars.direction === 1 ? -1 : 1;

    context.vars.directionMessage = JSON.stringify({
      type: "direction",
      direction: context.vars.direction,
      timestamp: Date.now()
    });

    context.vars.messagesSent++;
    return done();
  },

  // Attack with realistic frequency and cooldown
  maybeAttack: function(context, events, done) {
    const now = Date.now();

    // Attack cooldown of 2 seconds minimum
    if (context.vars.attacking || (now - context.vars.lastAttackTime) < 2000) {
      context.vars.attackMessage = null;
      return done();
    }

    // Only attack 5% of the time when not on cooldown
    if (Math.random() > 0.05) {
      context.vars.attackMessage = null;
      return done();
    }

    context.vars.attacking = true;
    context.vars.lastAttackTime = now;

    // Generate random attack position near player
    const attackX = context.vars.position.x + (Math.random() - 0.5) * 100;
    const attackY = context.vars.position.y + (Math.random() - 0.5) * 100;

    context.vars.attackMessage = JSON.stringify({
      type: "attack",
      position: { x: Math.floor(attackX), y: Math.floor(attackY) },
      timestamp: now
    });

    context.vars.messagesSent++;
    return done();
  },

  // End attack after realistic duration
  maybeAttackEnd: function(context, events, done) {
    if (!context.vars.attacking) {
      context.vars.attackEndMessage = null;
      return done();
    }

    // End attack after 200-500ms
    const attackDuration = Date.now() - context.vars.lastAttackTime;
    if (attackDuration < 200) {
      context.vars.attackEndMessage = null;
      return done();
    }

    context.vars.attacking = false;

    context.vars.attackEndMessage = JSON.stringify({
      type: "attackEnd",
      timestamp: Date.now()
    });

    context.vars.messagesSent++;
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
