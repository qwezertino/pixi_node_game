import type { UnitDefinition } from "../../shared/units";

export class StaminaPredictor {
    private value: number;

    constructor(private unit: UnitDefinition, initial: number = unit.stamina) {
        this.value = initial;
    }

    get current(): number {
        return this.value;
    }

    setUnit(unit: UnitDefinition): void {
        this.unit = unit;
    }

    update(deltaSeconds: number, intent: { blocking: boolean; sprinting: boolean }): void {
        let ratePerSecond: number;
        if (intent.blocking && this.unit.block) {
            ratePerSecond = -this.unit.block.drainPerSecond;
        } else if (intent.sprinting) {
            ratePerSecond = -this.unit.sprintStaminaCostPerSecond;
        } else {
            ratePerSecond = this.unit.staminaRegenPerSecond;
        }
        this.value = Math.min(this.unit.stamina, Math.max(0, this.value + ratePerSecond * deltaSeconds));
    }

    onAttackStart(): void {
        if (this.unit.attackStaminaCost) {
            this.value = Math.max(0, this.value - this.unit.attackStaminaCost);
        }
    }

    reconcile(serverValue: number): void {
        this.value = serverValue;
    }
}
