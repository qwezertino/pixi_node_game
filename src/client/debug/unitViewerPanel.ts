
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
`;

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

    unitSelect.addEventListener("change", () => void refreshAnimationOptions());
    sheetSelect.addEventListener("change", () => void refreshAnimationOptions());
    animationSelect.addEventListener("change", () => void renderSelection());
    directionSelect.addEventListener("change", () => void renderSelection());

    if (units.length > 0) {
        unitSelect.value = units[0].id;
        void refreshAnimationOptions();
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
        position: relative; width: min(1000px, 92vw); height: min(720px, 88vh);
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
