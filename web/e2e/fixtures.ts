import { spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { test as base, expect, type Page } from "@playwright/test";

/** A TCP port nothing listens on now (the demo keeps it across restarts). */
function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.once("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      srv.close(() => (addr && typeof addr === "object" ? resolve(addr.port) : reject(new Error("no port"))));
    });
  });
}

/** A `ghrm demo` process that can be stopped and started again on the same port and state. */
export class Demo {
  private proc?: ChildProcess;
  readonly dataDir = mkdtempSync(path.join(tmpdir(), "ghrm-demo-"));
  constructor(readonly port: number) {}

  get url() {
    return `http://127.0.0.1:${this.port}`;
  }

  async start() {
    const bin = process.env.GHRM_E2E_BIN;
    if (!bin) throw new Error("GHRM_E2E_BIN is not set (global setup did not run)");
    const proc = spawn(bin, ["demo", "--listen", `127.0.0.1:${this.port}`, "--data-dir", path.join(this.dataDir, "state"), "--seed", "42", "--tick", "250ms", "--job-seconds", "4-8"], {
      stdio: ["ignore", "ignore", "pipe"],
    });
    this.proc = proc;
    let stderr = "";
    proc.stderr?.on("data", (b: Buffer) => (stderr = (stderr + b.toString()).slice(-2000)));
    const deadline = Date.now() + 20_000;
    while (Date.now() < deadline) {
      // A demo that exited (the port was taken meanwhile) must not leave the tests talking
      // to whatever else answers on that port.
      if (proc.exitCode !== null) throw new Error(`the demo exited (${proc.exitCode}): ${stderr.trim()}`);
      try {
        if ((await fetch(`${this.url}/readyz`)).ok) return;
      } catch {
        // not listening yet
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    throw new Error("the demo did not become ready");
  }

  async stop() {
    const p = this.proc;
    if (!p || p.exitCode !== null) return;
    const exited = new Promise((r) => p.once("exit", r));
    p.kill("SIGTERM");
    await exited;
  }
}

/** Collects console errors and uncaught exceptions of a page. */
export function trackErrors(page: Page) {
  const errors: string[] = [];
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
  page.on("pageerror", (e) => errors.push(e.message));
  return errors;
}

/** The demo's account (ghrm demo creates it). */
export const demoAccount = { username: "admin", password: "demo-password" };

export const test = base.extend<object, { demo: Demo }>({
  // Every test runs signed in; the API request context shares the browser's cookies.
  context: async ({ context, demo }, provide) => {
    const r = await context.request.post(`${demo.url}/api/v1/auth/login`, { data: demoAccount });
    if (!r.ok()) throw new Error(`sign-in failed: ${r.status()} ${await r.text()}`);
    await provide(context);
  },
  // The standalone API client signs in too (it keeps the session cookie).
  request: async ({ request, demo }, provide) => {
    const r = await request.post(`${demo.url}/api/v1/auth/login`, { data: demoAccount });
    if (!r.ok()) throw new Error(`sign-in failed: ${r.status()} ${await r.text()}`);
    await provide(request);
  },
  demo: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      // A free port, not a fixed one: another local server may hold any given port.
      const demo = new Demo(await freePort());
      await demo.start();
      await use(demo);
      await demo.stop();
    },
    { scope: "worker" },
  ],
});

export { expect };

export const pages: [string, string][] = [
  ["/", "Overview"],
  ["/repositories", "Repositories"],
  ["/scale-sets", "Scale sets"],
  ["/templates", "Templates"],
  ["/templates?tab=history", "Templates"],
  ["/jobs", "Jobs"],
  ["/jobs?tab=history", "Jobs"],
  ["/environments", "Environments"],
  ["/environments?tab=history", "Environments"],
  ["/logs", "Events"],
  ["/settings", "Settings"],
];
