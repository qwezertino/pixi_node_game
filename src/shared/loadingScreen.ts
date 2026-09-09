
export function showLoadingError(message: string): void {
    const screen = document.getElementById("loading-screen");
    const text = document.getElementById("loading-text");
    if (screen) screen.classList.add("error");
    if (text) text.textContent = message;
}

export function hideLoadingScreen(): void {
    document.getElementById("loading-screen")?.classList.add("hidden");
}
