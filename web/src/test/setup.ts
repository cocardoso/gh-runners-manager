import "@testing-library/jest-dom/vitest";

// jsdom lacks these browser APIs that Kumo and TanStack Virtual use.
if (!window.matchMedia) {
  window.matchMedia = (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList;
}
if (!globalThis.ResizeObserver) {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}
if (!Element.prototype.scrollTo) Element.prototype.scrollTo = () => {};
if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
if (!Element.prototype.getAnimations) Element.prototype.getAnimations = () => [];

// Kumo reports misuse (unknown variants, missing labels) through console.warn: fail on it.
const warn = console.warn.bind(console);
console.warn = (...args: unknown[]) => {
  if (typeof args[0] === "string" && args[0].includes("[kumo]")) throw new Error(`Kumo warning: ${args.join(" ")}`);
  warn(...args);
};

// jsdom has no layout: give log viewports a size so TanStack Virtual renders rows.
for (const [prop, size] of [["offsetHeight", 400], ["offsetWidth", 800]] as const) {
  Object.defineProperty(HTMLElement.prototype, prop, {
    configurable: true,
    get(this: HTMLElement) {
      return this.getAttribute("role") === "log" ? size : 0;
    },
  });
}

// A list page's view (cards or list) is remembered in storage; each test starts from the default.
afterEach(() => {
  for (const key of Object.keys(localStorage)) if (key.startsWith("ghrm.view.")) localStorage.removeItem(key);
});
