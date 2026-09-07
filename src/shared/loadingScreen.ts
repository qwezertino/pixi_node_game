// Talks to the static #loading-screen markup in index.html. Game rules and
// unit stats now come from the server (GET /api/config, /api/units) instead
// of files bundled at build time, so the very first thing the client does is
// wait on a network round-trip — this gives that wait a visible state instead
// of a blank page, and a clear message if the server can't be reached at all.

export function showLoadingError(message: string): void {
    const screen = document.getElementById("loading-screen");
    const text = document.getElementById("loading-text");
    if (screen) screen.classList.add("error");
    if (text) text.textContent = message;
}

export function hideLoadingScreen(): void {
    document.getElementById("loading-screen")?.classList.add("hidden");
}
