import { Text, Application } from "pixi.js";

export class FpsDisplay {
    private text: Text;
    private app: Application;

    constructor(app: Application) {
        this.app = app;
        this.text = new Text({
            text: "FPS: 0",
            style: {
                fontFamily: "Arial",
                fontSize: 16,
                fill: 0xffffff,
                align: "left"
            }
        });
        this.text.position.set(10, 10);
        app.stage.addChild(this.text);
    }

    update() {
        this.text.text = `FPS: ${Math.round(this.app.ticker.FPS)}`;
    }
}