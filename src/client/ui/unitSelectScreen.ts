import { Application, Container, Graphics, Text, AnimatedSprite } from "pixi.js";
import { UNITS, type UnitDefinition } from "../../shared/units";
import { SpriteLoader, type CharacterVisual } from "../utils/spriteLoader";

const COLUMNS = 4;
const CARD_WIDTH = 180;
const CARD_HEIGHT = 220;
const CARD_GAP = 16;
const SPRITE_AREA_HEIGHT = 100;

const CARD_BG = 0x000000;
const CARD_BG_ALPHA = 0.75;
const BORDER_IDLE = 0x555555;
const BORDER_HOVER = 0xffcc00;

/**
 * Character-select screen (GDD-adjacent, no doc section of its own): shown once at
 * startup, before any network connection, so the server never spawns a player until
 * the user has actually chosen a unit. Each card plays that unit's real idle
 * animation (falling back to the placeholder knight the same way main.ts does for
 * in-game sprites, so a broken/missing sheet never leaves a card blank).
 */
export async function showUnitSelectScreen(app: Application): Promise<UnitDefinition> {
    const overlay = new Container();
    app.stage.addChild(overlay);

    const backdrop = new Graphics();
    backdrop.rect(0, 0, app.screen.width, app.screen.height);
    backdrop.fill({ color: 0x101010, alpha: 0.92 });
    overlay.addChild(backdrop);

    const title = new Text({
        text: "Выберите юнита",
        style: { fontFamily: "Arial", fontSize: 28, fill: 0xffffff, align: "center" },
    });
    title.anchor.set(0.5, 0);
    title.position.set(app.screen.width / 2, 24);
    overlay.addChild(title);

    const loadingText = new Text({
        text: "Загрузка...",
        style: { fontFamily: "Arial", fontSize: 16, fill: 0xaaaaaa },
    });
    loadingText.anchor.set(0.5);
    loadingText.position.set(app.screen.width / 2, app.screen.height / 2);
    overlay.addChild(loadingText);

    // Loaded in parallel, real sheet first — every unit falls back to the same
    // placeholder main.ts uses if its sheet fails to decode, so a broken asset never
    // leaves a card blank.
    const visuals = await Promise.all(
        UNITS.map((unit) =>
            SpriteLoader.loadUnitCharacterVisual(unit).catch(() =>
                SpriteLoader.loadCharacterVisual("/assets/16x16_knight_3_v3.png")
            )
        )
    );

    overlay.removeChild(loadingText);
    loadingText.destroy();

    const rows = Math.ceil(UNITS.length / COLUMNS);
    const gridWidth = COLUMNS * CARD_WIDTH + (COLUMNS - 1) * CARD_GAP;
    const gridHeight = rows * CARD_HEIGHT + (rows - 1) * CARD_GAP;
    const gridLeft = (app.screen.width - gridWidth) / 2;
    const gridTop = Math.max(80, (app.screen.height - gridHeight) / 2);

    return new Promise<UnitDefinition>((resolve) => {
        UNITS.forEach((unit, index) => {
            const col = index % COLUMNS;
            const row = Math.floor(index / COLUMNS);
            const card = buildUnitCard(unit, visuals[index], () => {
                overlay.destroy({ children: true });
                resolve(unit);
            });
            card.position.set(gridLeft + col * (CARD_WIDTH + CARD_GAP), gridTop + row * (CARD_HEIGHT + CARD_GAP));
            overlay.addChild(card);
        });
    });
}

function buildUnitCard(unit: UnitDefinition, visual: CharacterVisual, onSelect: () => void): Container {
    const card = new Container();
    card.eventMode = "static";
    card.cursor = "pointer";

    const background = new Graphics();
    const drawBackground = (borderColor: number) => {
        background.clear();
        background.roundRect(0, 0, CARD_WIDTH, CARD_HEIGHT, 8);
        background.fill({ color: CARD_BG, alpha: CARD_BG_ALPHA });
        background.stroke({ width: 2, color: borderColor });
    };
    drawBackground(BORDER_IDLE);
    card.addChild(background);

    const nameText = new Text({
        text: unit.displayName,
        style: { fontFamily: "Arial", fontSize: 14, fill: 0xffffff, align: "center" },
    });
    nameText.anchor.set(0.5, 0);
    nameText.position.set(CARD_WIDTH / 2, 8);
    card.addChild(nameText);

    const sprite = visual.getAnimation(visual.directional ? "idle_right" : "idle") as AnimatedSprite;
    sprite.animationSpeed = unit.animationSpeed;
    sprite.play();
    // Fit the sprite inside a fixed preview area regardless of the unit's own
    // in-game scale — a card is a much smaller frame than the game world.
    const frame = sprite.texture;
    const fitScale = Math.min(
        (CARD_WIDTH * 0.6) / frame.width,
        (SPRITE_AREA_HEIGHT * 0.9) / frame.height
    );
    sprite.scale.set(fitScale);
    sprite.position.set(CARD_WIDTH / 2, 28 + SPRITE_AREA_HEIGHT / 2);
    card.addChild(sprite);

    const stats = [
        `HP: ${unit.hp}`,
        `Урон: ${unit.damage}`,
        `Скорость: ${unit.moveSpeed.toFixed(1)}`,
        `Дальность: ${unit.range.toFixed(1)}м`,
        `Стамина: ${unit.stamina}`,
    ].join("\n");
    const statsText = new Text({
        text: stats,
        style: { fontFamily: "Courier New", fontSize: 12, fill: 0xdddddd, align: "left", lineHeight: 16 },
    });
    statsText.anchor.set(0.5, 0);
    statsText.position.set(CARD_WIDTH / 2, 28 + SPRITE_AREA_HEIGHT + 8);
    card.addChild(statsText);

    card.on("pointerover", () => drawBackground(BORDER_HOVER));
    card.on("pointerout", () => drawBackground(BORDER_IDLE));
    card.on("pointertap", onSelect);

    return card;
}
