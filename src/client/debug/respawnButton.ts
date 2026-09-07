/**
 * Dev-only "respawn" button: re-runs the unit-select + connect flow in
 * place, no page reload. Handy right after editing unit stats in the
 * "🧩 Units" panel — pick a unit again and see the new numbers immediately.
 */
export function mountRespawnButton(onRespawn: () => Promise<void>): void {
    const button = document.createElement("button");
    button.textContent = "🔄 Respawn";
    button.title = "Disconnect and pick a unit again (dev only)";
    button.style.cssText = `
        position: fixed; bottom: 12px; left: 120px; z-index: 10000;
        padding: 6px 12px; font-size: 12px; font-family: -apple-system, system-ui, sans-serif;
        background: #333; color: #eee; border: 1px solid #555; border-radius: 4px; cursor: pointer;
    `;

    let respawning = false;
    button.addEventListener("click", () => {
        if (respawning) return;
        respawning = true;
        button.disabled = true;
        button.textContent = "🔄 …";
        void onRespawn().finally(() => {
            respawning = false;
            button.disabled = false;
            button.textContent = "🔄 Respawn";
        });
    });

    document.body.appendChild(button);
}
