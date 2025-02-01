export interface World {
    createEntity(archetype: symbol): number;
    destroyEntity(entity: number): void;
    registerArchetype(...components: string[]): symbol;
  }

  export interface PhysicsWorkerMessage {
    type: 'tick' | 'input';
    state?: GameState;
    playerId?: number;
    dx?: number;
    dy?: number;
    seq?: number;
  }
