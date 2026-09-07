
import { Application, AnimatedSprite, Assets, Texture, Rectangle } from "pixi.js";
import { UNITS } from "../../shared/units";
import {
    NON_COMBAT_ANIMATIONS,
    type Direction,
    type NonCombatAnimation,
    type CombatAnimation,
    type FrameRect,
    getNonCombatFrameRects,
    getCombatFrameRects,
    combatAnimationsFor,
    gridFor,
} from "../utils/animationLayout";

const PANEL_STYLES = `
    font-family: -apple-system, system-ui, sans-serif;
    color: #eee;
    display: flex;
    height: 100%;
`;

const HTML = `
<div id="uv-controls" style="width:280px;flex:none;padding:16px;box-sizing:border-box;background:#222;overflow-y:auto;border-right:1px solid #333;">
    <h1 style="font-size:15px;margin:0 0 12px;">Unit Viewer — animation row QA</h1>
    <label style="display:block;font-size:12px;color:#aaa;margin:12px 0 4px;">Unit</label>
    <select id="uv-unit" style="width:100%;padding:6px;background:#333;color:#eee;border:1px solid #444;"></select>
    <label style="display:block;font-size:12px;color:#aaa;margin:12px 0 4px;">Sheet</label>
    <select id="uv-sheet" style="width:100%;padding:6px;background:#333;color:#eee;border:1px solid #444;">
        <option value="movement">Movement (non-combat)</option>
        <option value="combat">Combat</option>
    </select>
    <label style="display:block;font-size:12px;color:#aaa;margin:12px 0 4px;">Animation</label>
    <select id="uv-animation" style="width:100%;padding:6px;background:#333;color:#eee;border:1px solid #444;"></select>
    <label style="display:block;font-size:12px;color:#aaa;margin:12px 0 4px;">Direction</label>
    <select id="uv-direction" style="width:100%;padding:6px;background:#333;color:#eee;border:1px solid #444;">
        <option value="right">right</option>
        <option value="left">left</option>
        <option value="down">down</option>
        <option value="up">up</option>
    </select>
    <div id="uv-info" style="margin-top:16px;font-size:12px;line-height:1.6;color:#9c9;white-space:pre-wrap;"></div>
    <div id="uv-warning" style="margin-top:8px;font-size:12px;color:#e77;"></div>
</div>
<div id="uv-view" style="flex:1;overflow:auto;padding:20px;display:flex;gap:24px;align-items:flex-start;">
    <div style="background:#262626;border:1px solid #333;padding:12px;">
        <h2 style="font-size:12px;color:#aaa;margin:0 0 8px;">Full spritesheet (highlighted row = selected animation+direction)</h2>
        <div id="uv-sheetWrap" style="position:relative;display:inline-block;">
            <img id="uv-sheetImg" style="image-rendering:pixelated;display:block;" />
            <div id="uv-highlight" hidden style="position:absolute;border:2px solid #ff4081;box-shadow:0 0 0 1px rgba(0,0,0,0.6);pointer-events:none;"></div>
        </div>
    </div>
    <div style="background:#262626;border:1px solid #333;padding:12px;">
        <h2 style="font-size:12px;color:#aaa;margin:0 0 8px;">Looping preview</h2>
        <canvas id="uv-previewCanvas" width="256" height="256" style="background:#333;"></canvas>
    </div>
</div>
<div id="uv-stats" style="width:300px;flex:none;padding:16px;box-sizing:border-box;background:#222;overflow-y:auto;border-left:1px solid #333;">
    <h1 style="font-size:15px;margin:0 0 4px;">Balance stats</h1>
    <p style="font-size:11px;color:#999;margin:0 0 12px;line-height:1.5;">
        Writes to Postgres and applies live within moments — new spawns and
        per-tick stats pick it up immediately. Already-spawned players keep
        their current HP/stamina until they reconnect.
    </p>
    <label style="display:block;font-size:11px;color:#aaa;margin:0 0 2px;">Range type</label>
    <select id="uv-rangeType" style="width:100%;padding:5px;margin:0 0 10px;background:#333;color:#eee;border:1px solid #444;box-sizing:border-box;">
        <option value="melee">melee</option>
        <option value="ranged">ranged</option>
    </select>
    <div id="uv-stat-fields"></div>
    <div id="uv-bool-fields" style="margin:4px 0 8px;"></div>
    <h2 style="font-size:12px;color:#aaa;margin:14px 0 6px;border-top:1px solid #333;padding-top:10px;">Optional scalars</h2>
    <div id="uv-optional-fields"></div>
    <h2 style="font-size:12px;color:#aaa;margin:14px 0 6px;border-top:1px solid #333;padding-top:10px;">Combat profiles</h2>
    <div id="uv-groups"></div>
    <button id="uv-save" style="width:100%;margin-top:12px;padding:8px;background:#3a6;color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:13px;">Save to database</button>
    <div id="uv-save-status" style="margin-top:8px;font-size:11px;line-height:1.5;white-space:pre-wrap;"></div>
</div>
`;

// Always-present, non-nullable columns — required on every unit.
const CORE_FIELDS: Array<{ key: keyof CoreStatsForm; label: string; step?: string }> = [
    { key: "hp", label: "HP" },
    { key: "passiveDR", label: "Passive DR (0-1)", step: "0.01" },
    { key: "moveSpeed", label: "Move speed (m/s)", step: "0.1" },
    { key: "range", label: "Range (m)", step: "0.1" },
    { key: "damage", label: "Damage" },
    { key: "windupSeconds", label: "Windup (s)", step: "0.01" },
    { key: "activeSeconds", label: "Active (s)", step: "0.01" },
    { key: "recoverySeconds", label: "Recovery (s)", step: "0.01" },
    { key: "stamina", label: "Stamina" },
    { key: "staminaRegenPerSecond", label: "Stamina regen/s", step: "0.1" },
    { key: "sprintSpeedMultiplier", label: "Sprint speed x", step: "0.05" },
    { key: "sprintStaminaCostPerSecond", label: "Sprint cost/s", step: "0.1" },
    { key: "animationSpeed", label: "Animation speed", step: "0.01" },
    { key: "costWood", label: "Cost: wood" },
    { key: "costStone", label: "Cost: stone" },
    { key: "costIron", label: "Cost: iron" },
];

interface CoreStatsForm {
    hp: number;
    passiveDR: number;
    moveSpeed: number;
    range: number;
    damage: number;
    windupSeconds: number;
    activeSeconds: number;
    recoverySeconds: number;
    stamina: number;
    staminaRegenPerSecond: number;
    sprintSpeedMultiplier: number;
    sprintStaminaCostPerSecond: number;
    animationSpeed: number;
    costWood: number;
    costStone: number;
    costIron: number;
}

const BOOL_FIELDS: Array<{ key: "requiresRoyalGuard" | "cleave" | "hasBraceStance"; label: string }> = [
    { key: "requiresRoyalGuard", label: "Requires royal guard" },
    { key: "cleave", label: "Cleave" },
    { key: "hasBraceStance", label: "Has brace stance" },
];

// Nullable top-level scalars — empty input means "not set" (NULL in Postgres).
const OPTIONAL_FIELDS: Array<{ key: string; label: string; step?: string; integer?: boolean }> = [
    { key: "comboSteps", label: "Combo steps", integer: true },
    { key: "comboWindowSeconds", label: "Combo window (s)", step: "0.01" },
    { key: "attackStaminaCost", label: "Attack stamina cost", step: "0.1" },
    { key: "drawHoldThresholdSeconds", label: "Draw-hold threshold (s)", step: "0.01" },
    { key: "dodgeCostMultiplier", label: "Dodge cost x", step: "0.01" },
    { key: "antiShieldMultiplier", label: "Anti-shield x", step: "0.01" },
    { key: "antiWoodStructureMultiplier", label: "Anti-wood-structure x", step: "0.01" },
];

// Nested optional combat profiles — toggled on/off with a checkbox; unchecked
// means "this unit doesn't have this mechanic" (every column in the group
// gets written as NULL). See units.Definition / liveconfig.UnitStatsPatch.
interface GroupFieldSpec {
    key: string;
    label: string;
    step?: string;
    integer?: boolean;
    optional?: boolean; // nullable even while the group is enabled
}
interface GroupSpec {
    key: "block" | "positionalBonus" | "opportunistBow" | "rogueQuiver" | "recon" | "fireArrow" | "dashThrust";
    label: string;
    fields: GroupFieldSpec[];
}

const GROUPS: GroupSpec[] = [
    {
        key: "block", label: "Block",
        fields: [
            { key: "meleeDR", label: "Melee DR", step: "0.01" },
            { key: "rangedDR", label: "Ranged DR", step: "0.01" },
            { key: "drainPerSecond", label: "Drain/s", step: "0.1" },
            { key: "recoverySeconds", label: "Recovery (s)", step: "0.01", optional: true },
        ],
    },
    {
        key: "positionalBonus", label: "Positional bonus",
        fields: [
            { key: "staminaCostReductionPct", label: "Stamina cost reduction %", step: "0.01" },
            { key: "minNearbyAllies", label: "Min nearby allies", integer: true },
        ],
    },
    {
        key: "opportunistBow", label: "Opportunist bow",
        fields: [
            { key: "damage", label: "Damage" },
            { key: "range", label: "Range (m)", step: "0.1" },
            { key: "cooldownSeconds", label: "Cooldown (s)", step: "0.01" },
        ],
    },
    {
        key: "rogueQuiver", label: "Rogue quiver",
        fields: [
            { key: "damage", label: "Damage" },
            { key: "range", label: "Range (m)", step: "0.1" },
            { key: "charges", label: "Charges", integer: true },
            { key: "rechargeSeconds", label: "Recharge (s)", step: "0.01" },
            { key: "executeMultiplier", label: "Execute x", step: "0.01" },
            { key: "executeHpThresholdPct", label: "Execute HP threshold %", step: "0.01" },
        ],
    },
    {
        key: "recon", label: "Recon",
        fields: [
            { key: "viewRadiusBonusPct", label: "View radius bonus %", step: "0.01" },
            { key: "detectionRadiusMeters", label: "Detection radius (m)", step: "0.1" },
        ],
    },
    {
        key: "fireArrow", label: "Fire arrow",
        fields: [
            { key: "damage", label: "Damage" },
            { key: "structureDamageMultiplier", label: "Structure dmg x", step: "0.01" },
            { key: "woodCostPerShot", label: "Wood cost/shot", integer: true },
        ],
    },
    {
        key: "dashThrust", label: "Dash thrust",
        fields: [
            { key: "distanceMeters", label: "Distance (m)", step: "0.1" },
            { key: "windupSeconds", label: "Windup (s)", step: "0.01" },
            { key: "recoverySeconds", label: "Recovery (s)", step: "0.01" },
            { key: "damageMultiplier", label: "Damage x", step: "0.01" },
            { key: "cooldownSeconds", label: "Cooldown (s)", step: "0.01" },
        ],
    },
];

const DISPLAY_TARGET_WIDTH = 260;
const PREVIEW_TARGET_SIZE = 200;

function imageSize(path: string): Promise<{ width: number; height: number }> {
    return new Promise((resolve, reject) => {
        const img = new Image();
        img.onload = () => resolve({ width: img.naturalWidth, height: img.naturalHeight });
        img.onerror = reject;
        img.src = path;
    });
}

function boundingBox(rects: FrameRect[]): FrameRect {
    const minX = Math.min(...rects.map((r) => r.x));
    const minY = Math.min(...rects.map((r) => r.y));
    const maxX = Math.max(...rects.map((r) => r.x + r.width));
    const maxY = Math.max(...rects.map((r) => r.y + r.height));
    return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

/** Builds the panel's DOM inside `container` and wires it up. Safe to call once per container. */
export function createUnitViewerPanel(container: HTMLElement): void {
    container.style.cssText += PANEL_STYLES;
    container.innerHTML = HTML;

    const q = <T extends HTMLElement>(id: string) => container.querySelector(`#${id}`) as T;
    const unitSelect = q<HTMLSelectElement>("uv-unit");
    const sheetSelect = q<HTMLSelectElement>("uv-sheet");
    const animationSelect = q<HTMLSelectElement>("uv-animation");
    const directionSelect = q<HTMLSelectElement>("uv-direction");
    const sheetImg = q<HTMLImageElement>("uv-sheetImg");
    const highlight = q<HTMLDivElement>("uv-highlight");
    const info = q<HTMLDivElement>("uv-info");
    const warning = q<HTMLDivElement>("uv-warning");

    const units = UNITS.filter((u) => u.assetPath);
    for (const u of units) {
        const opt = document.createElement("option");
        opt.value = u.id;
        opt.textContent = `${u.displayName} (${u.id})`;
        unitSelect.appendChild(opt);
    }

    const statFieldsContainer = q<HTMLDivElement>("uv-stat-fields");
    const boolFieldsContainer = q<HTMLDivElement>("uv-bool-fields");
    const optionalFieldsContainer = q<HTMLDivElement>("uv-optional-fields");
    const groupsContainer = q<HTMLDivElement>("uv-groups");
    const rangeTypeSelect = q<HTMLSelectElement>("uv-rangeType");
    const saveButton = q<HTMLButtonElement>("uv-save");
    const saveStatus = q<HTMLDivElement>("uv-save-status");
    const statInputs = new Map<keyof CoreStatsForm, HTMLInputElement>();
    const boolInputs = new Map<string, HTMLInputElement>();
    const optionalInputs = new Map<string, HTMLInputElement>();
    const groupEnabled = new Map<GroupSpec["key"], HTMLInputElement>();
    const groupInputs = new Map<GroupSpec["key"], Map<string, HTMLInputElement>>();

    function numberInput(step?: string): HTMLInputElement {
        const input = document.createElement("input");
        input.type = "number";
        if (step) input.step = step;
        input.style.cssText = "width:100%;padding:5px;background:#333;color:#eee;border:1px solid #444;box-sizing:border-box;";
        return input;
    }

    function fieldRow(label: string, input: HTMLElement): HTMLDivElement {
        const row = document.createElement("div");
        row.style.cssText = "margin:0 0 8px;";
        const labelEl = document.createElement("label");
        labelEl.textContent = label;
        labelEl.style.cssText = "display:block;font-size:11px;color:#aaa;margin:0 0 2px;";
        row.appendChild(labelEl);
        row.appendChild(input);
        return row;
    }

    for (const field of CORE_FIELDS) {
        const input = numberInput(field.step);
        statFieldsContainer.appendChild(fieldRow(field.label, input));
        statInputs.set(field.key, input);
    }

    for (const field of BOOL_FIELDS) {
        const row = document.createElement("label");
        row.style.cssText = "display:flex;align-items:center;gap:6px;font-size:12px;color:#ccc;margin:0 0 6px;cursor:pointer;";
        const input = document.createElement("input");
        input.type = "checkbox";
        row.appendChild(input);
        row.appendChild(document.createTextNode(field.label));
        boolFieldsContainer.appendChild(row);
        boolInputs.set(field.key, input);
    }

    for (const field of OPTIONAL_FIELDS) {
        const input = numberInput(field.step);
        if (field.integer) input.step = "1";
        optionalFieldsContainer.appendChild(fieldRow(field.label, input));
        optionalInputs.set(field.key, input);
    }

    for (const group of GROUPS) {
        const section = document.createElement("div");
        section.style.cssText = "margin:0 0 12px;padding:8px;background:#262626;border:1px solid #333;border-radius:4px;";

        const header = document.createElement("label");
        header.style.cssText = "display:flex;align-items:center;gap:6px;font-size:12px;color:#ddd;font-weight:600;margin:0 0 8px;cursor:pointer;";
        const enableInput = document.createElement("input");
        enableInput.type = "checkbox";
        header.appendChild(enableInput);
        header.appendChild(document.createTextNode(group.label));
        section.appendChild(header);
        groupEnabled.set(group.key, enableInput);

        const fieldsWrap = document.createElement("div");
        const inputs = new Map<string, HTMLInputElement>();
        for (const field of group.fields) {
            const input = numberInput(field.step);
            if (field.integer) input.step = "1";
            fieldsWrap.appendChild(fieldRow(field.label + (field.optional ? " (optional)" : ""), input));
            inputs.set(field.key, input);
        }
        section.appendChild(fieldsWrap);
        groupInputs.set(group.key, inputs);

        const syncDisabled = () => {
            fieldsWrap.style.opacity = enableInput.checked ? "1" : "0.4";
            for (const input of inputs.values()) input.disabled = !enableInput.checked;
        };
        enableInput.addEventListener("change", syncDisabled);
        syncDisabled();

        groupsContainer.appendChild(section);
    }

    function populateStatsForm(unit: (typeof units)[number]) {
        statInputs.get("hp")!.value = String(unit.hp);
        statInputs.get("passiveDR")!.value = String(unit.passiveDR);
        statInputs.get("moveSpeed")!.value = String(unit.moveSpeed);
        statInputs.get("range")!.value = String(unit.range);
        statInputs.get("damage")!.value = String(unit.damage);
        statInputs.get("windupSeconds")!.value = String(unit.windupSeconds);
        statInputs.get("activeSeconds")!.value = String(unit.activeSeconds);
        statInputs.get("recoverySeconds")!.value = String(unit.recoverySeconds);
        statInputs.get("stamina")!.value = String(unit.stamina);
        statInputs.get("staminaRegenPerSecond")!.value = String(unit.staminaRegenPerSecond);
        statInputs.get("sprintSpeedMultiplier")!.value = String(unit.sprintSpeedMultiplier);
        statInputs.get("sprintStaminaCostPerSecond")!.value = String(unit.sprintStaminaCostPerSecond);
        statInputs.get("animationSpeed")!.value = String(unit.animationSpeed);
        statInputs.get("costWood")!.value = String(unit.cost.wood);
        statInputs.get("costStone")!.value = String(unit.cost.stone);
        statInputs.get("costIron")!.value = String(unit.cost.iron);

        rangeTypeSelect.value = unit.rangeType;

        boolInputs.get("requiresRoyalGuard")!.checked = unit.requiresRoyalGuard ?? false;
        boolInputs.get("cleave")!.checked = unit.cleave ?? false;
        boolInputs.get("hasBraceStance")!.checked = unit.hasBraceStance ?? false;

        const unitRecord = unit as unknown as Record<string, unknown>;
        for (const field of OPTIONAL_FIELDS) {
            const value = unitRecord[field.key];
            optionalInputs.get(field.key)!.value = value === undefined || value === null ? "" : String(value);
        }

        for (const group of GROUPS) {
            const groupValue = unitRecord[group.key] as Record<string, unknown> | undefined;
            const enableInput = groupEnabled.get(group.key)!;
            enableInput.checked = groupValue !== undefined && groupValue !== null;
            const inputs = groupInputs.get(group.key)!;
            for (const field of group.fields) {
                const value = groupValue?.[field.key];
                inputs.get(field.key)!.value = value === undefined || value === null ? "" : String(value);
            }
            enableInput.dispatchEvent(new Event("change"));
        }

        saveStatus.textContent = "";
        saveStatus.style.color = "#9c9";
    }

    saveButton.addEventListener("click", () => {
        void (async () => {
            const unit = units.find((u) => u.id === unitSelect.value);
            if (!unit) return;

            const body: Record<string, unknown> = {
                hp: Number(statInputs.get("hp")!.value),
                passiveDR: Number(statInputs.get("passiveDR")!.value),
                moveSpeed: Number(statInputs.get("moveSpeed")!.value),
                rangeType: rangeTypeSelect.value,
                range: Number(statInputs.get("range")!.value),
                damage: Number(statInputs.get("damage")!.value),
                windupSeconds: Number(statInputs.get("windupSeconds")!.value),
                activeSeconds: Number(statInputs.get("activeSeconds")!.value),
                recoverySeconds: Number(statInputs.get("recoverySeconds")!.value),
                stamina: Number(statInputs.get("stamina")!.value),
                staminaRegenPerSecond: Number(statInputs.get("staminaRegenPerSecond")!.value),
                sprintSpeedMultiplier: Number(statInputs.get("sprintSpeedMultiplier")!.value),
                sprintStaminaCostPerSecond: Number(statInputs.get("sprintStaminaCostPerSecond")!.value),
                animationSpeed: Number(statInputs.get("animationSpeed")!.value),
                cost: {
                    wood: Number(statInputs.get("costWood")!.value),
                    stone: Number(statInputs.get("costStone")!.value),
                    iron: Number(statInputs.get("costIron")!.value),
                },
                requiresRoyalGuard: boolInputs.get("requiresRoyalGuard")!.checked,
                cleave: boolInputs.get("cleave")!.checked,
                hasBraceStance: boolInputs.get("hasBraceStance")!.checked,
            };

            for (const field of OPTIONAL_FIELDS) {
                const raw = optionalInputs.get(field.key)!.value;
                body[field.key] = raw === "" ? null : Number(raw);
            }

            for (const group of GROUPS) {
                const enabled = groupEnabled.get(group.key)!.checked;
                if (!enabled) {
                    body[group.key] = null;
                    continue;
                }
                const inputs = groupInputs.get(group.key)!;
                const groupBody: Record<string, unknown> = {};
                for (const field of group.fields) {
                    const raw = inputs.get(field.key)!.value;
                    groupBody[field.key] = raw === "" ? (field.optional ? null : 0) : Number(raw);
                }
                body[group.key] = groupBody;
            }

            saveButton.disabled = true;
            saveStatus.style.color = "#9c9";
            saveStatus.textContent = "Saving…";
            try {
                const res = await fetch(`/api/admin/units/${unit.typeId}`, {
                    method: "PATCH",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify(body),
                });
                const text = await res.text();
                if (!res.ok) throw new Error(text || `status ${res.status}`);
                const parsed = JSON.parse(text) as { note?: string };
                saveStatus.style.color = "#9c9";
                saveStatus.textContent = parsed.note ?? "Saved.";
            } catch (err) {
                saveStatus.style.color = "#e77";
                saveStatus.textContent = `Failed: ${err instanceof Error ? err.message : String(err)}`;
            } finally {
                saveButton.disabled = false;
            }
        })();
    });

    const app = new Application();
    let appReady = false;
    let previewSprite: AnimatedSprite | null = null;

    async function ensureApp() {
        if (appReady) return;
        await app.init({
            canvas: q<HTMLCanvasElement>("uv-previewCanvas"),
            width: 256,
            height: 256,
            background: "#333333",
        });
        appReady = true;
    }

    function currentPath(): { unit: (typeof units)[number]; sheet: "movement" | "combat"; path?: string } | null {
        const unit = units.find((u) => u.id === unitSelect.value);
        if (!unit) return null;
        const sheet = sheetSelect.value as "movement" | "combat";
        return { unit, sheet, path: sheet === "movement" ? unit.assetPath : unit.combatAssetPath };
    }

    async function refreshAnimationOptions() {
        const current = currentPath();
        if (!current || !current.path) {
            animationSelect.innerHTML = "";
            void renderSelection();
            return;
        }

        const { width, height } = await imageSize(`/assets/${current.path}`);
        const names: readonly string[] =
            current.sheet === "movement" ? NON_COMBAT_ANIMATIONS : combatAnimationsFor(width, height) ?? [];

        const previouslySelected = animationSelect.value;
        animationSelect.innerHTML = "";
        for (const name of names) {
            const opt = document.createElement("option");
            opt.value = name;
            opt.textContent = name;
            animationSelect.appendChild(opt);
        }
        if (names.includes(previouslySelected)) {
            animationSelect.value = previouslySelected;
        }

        void renderSelection();
    }

    async function renderSelection() {
        const current = currentPath();
        if (!current) {
            info.textContent = "No unit selected.";
            warning.textContent = "";
            highlight.hidden = true;
            return;
        }
        const { unit, sheet, path } = current;
        if (!path) {
            info.textContent = `${unit.displayName} has no ${sheet} sheet.`;
            warning.textContent = "";
            highlight.hidden = true;
            return;
        }
        const animation = animationSelect.value;
        if (!animation) {
            info.textContent = `${unit.displayName}: no animations available on the ${sheet} sheet.`;
            warning.textContent = "";
            highlight.hidden = true;
            return;
        }

        const url = `/assets/${path}`;
        const { width, height } = await imageSize(url);
        const grid = gridFor(width, height);
        const direction = directionSelect.value as Direction;

        const rects =
            sheet === "movement"
                ? getNonCombatFrameRects(width, height, animation as NonCombatAnimation, direction)
                : getCombatFrameRects(width, height, animation as CombatAnimation, direction);

        sheetImg.src = url;
        const scale = DISPLAY_TARGET_WIDTH / width;
        sheetImg.style.width = `${width * scale}px`;
        sheetImg.style.height = `${height * scale}px`;

        const rowIndex = rects ? Math.round(rects[0].y / grid.cellSize) : undefined;
        info.textContent =
            `unit: ${unit.id}\n` +
            `sheet: ${path}\n` +
            `size: ${width}x${height}, cell: ${grid.cellSize}px, cols: ${grid.columns}, rows: ${grid.rows}\n` +
            `animation: ${animation}  direction: ${direction}\n` +
            `frames: ${rects ? rects.length : 0}` +
            (rowIndex !== undefined ? `  (row ${rowIndex})` : "");

        if (!rects) {
            warning.textContent = "This (animation, direction) is out of range for this sheet — not drawn.";
            highlight.hidden = true;
            if (previewSprite) previewSprite.visible = false;
            return;
        }
        warning.textContent = "";

        const bounds = boundingBox(rects);
        highlight.hidden = false;
        highlight.style.left = `${bounds.x * scale}px`;
        highlight.style.top = `${bounds.y * scale}px`;
        highlight.style.width = `${bounds.width * scale}px`;
        highlight.style.height = `${bounds.height * scale}px`;

        await ensureApp();
        const texture = await Assets.load(url);
        texture.source.scaleMode = "nearest";
        const frames = rects.map((r) => new Texture({ source: texture.source, frame: new Rectangle(r.x, r.y, r.width, r.height) }));

        if (previewSprite) {
            app.stage.removeChild(previewSprite);
            previewSprite.destroy();
        }
        previewSprite = new AnimatedSprite(frames);
        previewSprite.anchor.set(0.5);
        previewSprite.position.set(128, 128);

        previewSprite.scale.set(Math.max(1, Math.round(PREVIEW_TARGET_SIZE / grid.cellSize)));
        previewSprite.animationSpeed = 0.12;
        previewSprite.play();
        app.stage.addChild(previewSprite);
    }

    unitSelect.addEventListener("change", () => {
        void refreshAnimationOptions();
        const unit = units.find((u) => u.id === unitSelect.value);
        if (unit) populateStatsForm(unit);
    });
    sheetSelect.addEventListener("change", () => void refreshAnimationOptions());
    animationSelect.addEventListener("change", () => void renderSelection());
    directionSelect.addEventListener("change", () => void renderSelection());

    if (units.length > 0) {
        unitSelect.value = units[0].id;
        void refreshAnimationOptions();
        populateStatsForm(units[0]);
    }
}

/**
 * Adds a floating "Units" button to the page that toggles a modal dialog hosting the
 * same panel as unit-viewer.html — a dimmed backdrop over the game rather than a
 * full-screen takeover, so the game screen stays visible/present behind it. The
 * panel is built lazily on first open so it never costs anything for players who
 * never touch it.
 */
export function mountUnitViewerToggle(): void {
    const button = document.createElement("button");
    button.textContent = "🧩 Units";
    button.style.cssText = `
        position: fixed; bottom: 12px; left: 12px; z-index: 10000;
        padding: 6px 12px; font-size: 12px; font-family: -apple-system, system-ui, sans-serif;
        background: #333; color: #eee; border: 1px solid #555; border-radius: 4px; cursor: pointer;
    `;

    const backdrop = document.createElement("div");
    backdrop.style.cssText = `
        position: fixed; inset: 0; z-index: 9999; background: rgba(0, 0, 0, 0.6);
        align-items: center; justify-content: center;
        display: none;
    `;

    const dialog = document.createElement("div");
    dialog.style.cssText = `
        position: relative; width: min(1440px, 96vw); height: min(800px, 92vh);
        background: #1b1b1b; border: 1px solid #444; border-radius: 6px; overflow: hidden;
        box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
    `;
    backdrop.appendChild(dialog);

    const closeButton = document.createElement("button");
    closeButton.textContent = "✕ Close";
    closeButton.style.cssText = `
        position: absolute; top: 10px; right: 16px; z-index: 10001;
        padding: 4px 10px; font-size: 12px; font-family: -apple-system, system-ui, sans-serif;
        background: #333; color: #eee; border: 1px solid #555; border-radius: 4px; cursor: pointer;
    `;
    dialog.appendChild(closeButton);

    const panelContainer = document.createElement("div");
    panelContainer.style.cssText = "position:absolute; inset:0;";
    dialog.appendChild(panelContainer);

    document.body.appendChild(backdrop);
    document.body.appendChild(button);

    let built = false;
    const open = () => {
        if (!built) {
            createUnitViewerPanel(panelContainer);
            built = true;
        }
        backdrop.style.display = "flex";
    };
    const close = () => {
        backdrop.style.display = "none";
    };

    button.addEventListener("click", open);
    closeButton.addEventListener("click", close);

    backdrop.addEventListener("click", (e) => {
        if (e.target === backdrop) close();
    });
}
