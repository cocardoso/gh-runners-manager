import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app";
import { applyTheme } from "./lib/theme";
import "./styles.css";

applyTheme((localStorage.getItem("ghrm.theme") as "light" | "dark" | null) ?? "system");

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
