// Entry point for the standalone unit-viewer.html page — no game/network connection.
// Actual implementation is shared with the in-game "Units" debug button, see
// debug/unitViewerPanel.ts.

import { createUnitViewerPanel } from "./debug/unitViewerPanel";

createUnitViewerPanel(document.body);
