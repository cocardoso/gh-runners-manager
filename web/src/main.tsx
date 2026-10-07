import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";

export function Root() {
  return <div className="p-6 text-kumo-default">gh-runners-manager</div>;
}

const el = document.getElementById("root");
if (el) {
  createRoot(el).render(
    <StrictMode>
      <Root />
    </StrictMode>,
  );
}
