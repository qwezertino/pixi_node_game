import type { UnitDefinition } from "../../shared/units";

// Stamina used to only reach the client via UNIT_ROSTER, a low-frequency channel
// (sent on connect + once per full-sync cycle, ~30s) that was fine back when nothing
// drained stamina — it always sat at max. Now that sprint/block/attacks spend it
// (see server world.go), that cadence is far too slow: the bar would sit frozen and
// then jump. This predicts the same drain/regen locally every frame from the unit's
// own stats — mirroring server world.go's rates continuously rather than per
// discrete tick, which is precise enough for a display value — and snaps to the
// authoritative number whenever a roster update arrives, the same "predict, then
// correct" shape movement prediction already uses for position.
export class StaminaPredictor {
    private value: number;

    constructor(private unit: UnitDefinition, initial: number = unit.stamina) {
        this.value = initial;
    }

    get current(): number {
        return this.value;
    }

    // Call when the player's assigned unit changes (e.g. roster backfill) so the
    // predictor uses the right max/regen/block rates going forward.
    setUnit(unit: UnitDefinition): void {
        this.unit = unit;
    }

    // Call once per rendered frame with the player's current intent for that frame.
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

    // Call the instant a swing is confirmed (server-authoritative attack start) so
    // the cost shows up immediately instead of waiting for the next correction.
    onAttackStart(): void {
        if (this.unit.attackStaminaCost) {
            this.value = Math.max(0, this.value - this.unit.attackStaminaCost);
        }
    }

    // Snap to the authoritative value (UNIT_ROSTER). A direct snap, not a replay —
    // stamina is a continuous rate, not a queued-input simulation like position.
    reconcile(serverValue: number): void {
        this.value = serverValue;
    }
}
