import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";

import { AppRouter } from "./app/router";
import { SessionProvider } from "./features/auth/SessionProvider";
import { ThemeProvider } from "./features/theme/ThemeProvider";
import {
  applyTheme,
  browserStorage,
  readStoredPreference,
  resolveTheme,
  systemPrefersDark
} from "./lib/theme";
import "./styles/globals.css";

const root = document.getElementById("root");

if (!root) {
  throw new Error("dashboard root element is missing");
}

// Applied before React mounts. The bundle finishes executing after the body has
// already been painted with `bg-background`, so leaving this to the provider's
// first commit shows a flash of the wrong theme on every load.
applyTheme(resolveTheme(readStoredPreference(browserStorage()), systemPrefersDark()));

// The server serves index.html for any unmatched path
// (`internal/app/app.go:97`), so real paths survive a hard refresh and no
// hash-router fallback is needed.
ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <ThemeProvider>
      <BrowserRouter>
        <SessionProvider>
          <AppRouter />
        </SessionProvider>
      </BrowserRouter>
    </ThemeProvider>
  </React.StrictMode>
);
