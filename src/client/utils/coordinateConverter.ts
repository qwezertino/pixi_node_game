import { WORLD } from "../../shared/gameConfig";


export class CoordinateConverter {
    private scaleX: number = 1;
    private scaleY: number = 1;
    private screenWidth: number;
    private screenHeight: number;

    constructor(screenWidth: number, screenHeight: number) {
        this.screenWidth = screenWidth;
        this.screenHeight = screenHeight;
        this.calculateScales();
    }

    /**
     * Рассчитываем коэффициенты конвертации на основе размера экрана
     * Используем подход "fill screen" - растягиваем виртуальный мир на весь экран
     */
    private calculateScales(): void {
        // Рассчитываем коэффициенты так, чтобы виртуальный мир заполнял весь экран
        const virtualWidth = WORLD.virtualSize.width;
        const virtualHeight = WORLD.virtualSize.height;

        // Рассчитываем коэффициенты для заполнения экрана
        this.scaleX = this.screenWidth / virtualWidth;
        this.scaleY = this.screenHeight / virtualHeight;
    }

    /**
     * Конвертировать виртуальные координаты в экранные пиксели
     * Мир заполняет весь экран, поэтому смещение не нужно
     * @param virtualX - X координата в виртуальном мире (0-6000)
     * @param virtualY - Y координата в виртуальном мире (0-6000)
     * @returns Экранные координаты в пикселях
     */
    virtualToScreen(virtualX: number, virtualY: number): { x: number, y: number } {
        return {
            x: virtualX * this.scaleX,
            y: virtualY * this.scaleY
        };
    }

    /**
     * Конвертировать экранные пиксели в виртуальные координаты
     * Мир заполняет весь экран, поэтому смещение не нужно
     * @param screenX - X координата на экране
     * @param screenY - Y координата на экране
     * @returns Виртуальные координаты (округленные до целых)
     */
    screenToVirtual(screenX: number, screenY: number): { x: number, y: number } {
        return {
            x: Math.round(screenX / this.scaleX),
            y: Math.round(screenY / this.scaleY)
        };
    }

    /**
     * Получить центр экрана в виртуальных координатах
     * Центр экрана соответствует центру виртуального мира
     */
    getVirtualCenter(): { x: number, y: number } {
        return {
            x: Math.round(WORLD.virtualSize.width / 2),
            y: Math.round(WORLD.virtualSize.height / 2)
        };
    }

    /**
     * Получить позицию виртуального мира на экране
     * Мир заполняет весь экран, поэтому смещение всегда (0, 0)
     */
    getWorldOffset(): { x: number, y: number } {
        return { x: 0, y: 0 };
    }

    /**
     * Проверить, находится ли точка в пределах виртуального мира
     */
    isInVirtualBounds(virtualX: number, virtualY: number): boolean {
        return virtualX >= WORLD.boundaries.minX &&
               virtualX <= WORLD.boundaries.maxX &&
               virtualY >= WORLD.boundaries.minY &&
               virtualY <= WORLD.boundaries.maxY;
    }

    /**
     * Ограничить координаты пределами виртуального мира
     */
    clampToVirtualBounds(virtualX: number, virtualY: number): { x: number, y: number } {
        return {
            x: Math.max(WORLD.boundaries.minX,
               Math.min(WORLD.boundaries.maxX, virtualX)),
            y: Math.max(WORLD.boundaries.minY,
               Math.min(WORLD.boundaries.maxY, virtualY))
        };
    }

    /**
     * Обновить размеры экрана и пересчитать коэффициенты
     */
    updateScreenSize(screenWidth: number, screenHeight: number): void {
        if (this.screenWidth !== screenWidth || this.screenHeight !== screenHeight) {
            this.screenWidth = screenWidth;
            this.screenHeight = screenHeight;
            this.calculateScales();
        }
    }

    /**
     * Получить текущие размеры экрана
     */
    getScreenSize(): { width: number, height: number } {
        return {
            width: this.screenWidth,
            height: this.screenHeight
        };
    }








}
