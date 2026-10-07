import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

/** Builds the ghrm binary with the current web/dist embedded. */
export default function globalSetup() {
  const root = path.resolve(import.meta.dirname, "../..");
  if (!existsSync(path.join(root, "web/dist/index.html"))) throw new Error("web/dist is not built: run `pnpm build` first");
  const bin = path.join(mkdtempSync(path.join(tmpdir(), "ghrm-e2e-")), "ghrm");
  execFileSync("go", ["build", "-o", bin, "./cmd/ghrm"], { cwd: root, stdio: "inherit", env: { ...process.env, CGO_ENABLED: "0" } });
  process.env.GHRM_E2E_BIN = bin;
}
