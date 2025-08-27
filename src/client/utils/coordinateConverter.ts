import { WORLD } from "../../common/gameSettings";

/**
 * Конвертер между целочисленными виртуальными координатами и экранными пикселями
 *
 * Виртуальный мир: 1000x1000 целочисленных единиц
 * Экран: адаптируется к размеру экрана каждого клиента
 *
 * Коэффициенты конвертации рассчитываются динамически для каждого клиента
 * Это обеспечивает точную синхронизацию без проблем с floating point precision
 */
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
     * Используем подход "fit to screen" - вписываем виртуальный мир в экран
     */
    private calculateScales(): void {
        // Рассчитываем коэффициенты так, чтобы виртуальный мир вписывался в экран
        // Сохраняя соотношение сторон виртуального мира
        const virtualWidth = WORLD.VIRTUAL_SIZE.WIDTH;
        const virtualHeight = WORLD.VIRTUAL_SIZE.HEIGHT;

        // Рассчитываем коэффициенты для вписывания по ширине и высоте
        const scaleXByWidth = this.screenWidth / virtualWidth;
        const scaleYByHeight = this.screenHeight / virtualHeight;

        // Выбираем минимальный коэффициент, чтобы весь мир поместился на экране
        const minScale = Math.min(scaleXByWidth, scaleYByHeight);

        this.scaleX = minScale;
        this.scaleY = minScale;
    }

    /**
     * Конвертировать виртуальные координаты в экранные пиксели
     * Учитывает смещение мира для центрирования на экране
     * @param virtualX - X координата в виртуальном мире (0-1000)
     * @param virtualY - Y координата в виртуальном мире (0-1000)
     * @returns Экранные координаты в пикселях
     */
    virtualToScreen(virtualX: number, virtualY: number): { x: number, y: number } {
        const offset = this.getWorldOffset();
        return {
            x: virtualX * this.scaleX + offset.x,
            y: virtualY * this.scaleY + offset.y
        };
    }

    /**
     * Конвертировать экранные пиксели в виртуальные координаты
     * Учитывает смещение мира на экране
     * @param screenX - X координата на экране
     * @param screenY - Y координата на экране
     * @returns Виртуальные координаты (округленные до целых)
     */
    screenToVirtual(screenX: number, screenY: number): { x: number, y: number } {
        const offset = this.getWorldOffset();
        return {
            x: Math.round((screenX - offset.x) / this.scaleX),
            y: Math.round((screenY - offset.y) / this.scaleY)
        };
    }

    /**
     * Получить центр экрана в виртуальных координатах
     * Центр экрана соответствует центру виртуального мира
     */
    getVirtualCenter(): { x: number, y: number } {
        return {
            x: Math.round(WORLD.VIRTUAL_SIZE.WIDTH / 2),
            y: Math.round(WORLD.VIRTUAL_SIZE.HEIGHT / 2)
        };
    }

    /**
     * Получить позицию виртуального мира на экране
     * Возвращает смещение, необходимое для центрирования мира на экране
     */
    getWorldOffset(): { x: number, y: number } {
        const virtualWidth = WORLD.VIRTUAL_SIZE.WIDTH * this.scaleX;
        const virtualHeight = WORLD.VIRTUAL_SIZE.HEIGHT * this.scaleY;

        return {
            x: (this.screenWidth - virtualWidth) / 2,
            y: (this.screenHeight - virtualHeight) / 2
        };
    }

    /**
     * Проверить, находится ли точка в пределах виртуального мира
     */
    isInVirtualBounds(virtualX: number, virtualY: number): boolean {
        return virtualX >= WORLD.BOUNDARIES.MIN_X &&
               virtualX <= WORLD.BOUNDARIES.MAX_X &&
               virtualY >= WORLD.BOUNDARIES.MIN_Y &&
               virtualY <= WORLD.BOUNDARIES.MAX_Y;
    }

    /**
     * Ограничить координаты пределами виртуального мира
     */
    clampToVirtualBounds(virtualX: number, virtualY: number): { x: number, y: number } {
        return {
            x: Math.max(WORLD.BOUNDARIES.MIN_X,
               Math.min(WORLD.BOUNDARIES.MAX_X, virtualX)),
            y: Math.max(WORLD.BOUNDARIES.MIN_Y,
               Math.min(WORLD.BOUNDARIES.MAX_Y, virtualY))
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
