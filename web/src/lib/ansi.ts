export type AnsiColor =
  | "black" | "red" | "green" | "yellow" | "blue" | "magenta" | "cyan" | "white"
  | "bright-black" | "bright-red" | "bright-green" | "bright-yellow" | "bright-blue" | "bright-magenta" | "bright-cyan" | "bright-white";

export interface AnsiSegment {
  text: string;
  fg?: AnsiColor;
  bg?: AnsiColor;
  fgRgb?: string;
  bgRgb?: string;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

type Style = Omit<AnsiSegment, "text">;

const NAMES = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"] as const;
const CUBE = [0, 95, 135, 175, 215, 255];

// CSI (ESC [ ... final), OSC (ESC ] ... BEL or ESC \), and lone two-byte escapes.
// eslint-disable-next-line no-control-regex
const ESCAPES = /\u001b\[([0-9;?]*)([@-~])|\u001b\][^\u0007\u001b]*(?:\u0007|\u001b\\)?|\u001b\[[0-9;?]*$|\u001b[@-_]?/g;

function color256(n: number): { name?: AnsiColor; rgb?: string } {
  if (n < 8) return { name: NAMES[n] };
  if (n < 16) return { name: `bright-${NAMES[n - 8]}` as AnsiColor };
  if (n < 232) {
    const i = n - 16;
    return { rgb: `rgb(${CUBE[Math.floor(i / 36)]},${CUBE[Math.floor(i / 6) % 6]},${CUBE[i % 6]})` };
  }
  const v = 8 + (n - 232) * 10;
  return { rgb: `rgb(${v},${v},${v})` };
}

function apply(style: Style, params: number[]): Style {
  const s = { ...style };
  for (let i = 0; i < params.length; i++) {
    const p = params[i]!;
    if (p === 0) for (const k of Object.keys(s)) delete s[k as keyof Style];
    else if (p === 1) s.bold = true;
    else if (p === 2) s.dim = true;
    else if (p === 3) s.italic = true;
    else if (p === 4) s.underline = true;
    else if (p === 22) delete s.bold, delete s.dim;
    else if (p === 23) delete s.italic;
    else if (p === 24) delete s.underline;
    else if (p >= 30 && p <= 37) (s.fg = NAMES[p - 30]), delete s.fgRgb;
    else if (p >= 90 && p <= 97) (s.fg = `bright-${NAMES[p - 90]}` as AnsiColor), delete s.fgRgb;
    else if (p >= 40 && p <= 47) (s.bg = NAMES[p - 40]), delete s.bgRgb;
    else if (p >= 100 && p <= 107) (s.bg = `bright-${NAMES[p - 100]}` as AnsiColor), delete s.bgRgb;
    else if (p === 39) delete s.fg, delete s.fgRgb;
    else if (p === 49) delete s.bg, delete s.bgRgb;
    else if (p === 38 || p === 48) {
      const fg = p === 38;
      let c: { name?: AnsiColor; rgb?: string } | undefined;
      if (params[i + 1] === 5 && params[i + 2] !== undefined) {
        c = color256(params[i + 2]!);
        i += 2;
      } else if (params[i + 1] === 2 && params[i + 4] !== undefined) {
        c = { rgb: `rgb(${params[i + 2]},${params[i + 3]},${params[i + 4]})` };
        i += 4;
      }
      if (c) {
        if (fg) {
          delete s.fg, delete s.fgRgb;
          if (c.name) s.fg = c.name;
          else s.fgRgb = c.rgb;
        } else {
          delete s.bg, delete s.bgRgb;
          if (c.name) s.bg = c.name;
          else s.bgRgb = c.rgb;
        }
      }
    }
  }
  return s;
}

function sameStyle(seg: AnsiSegment, style: Style) {
  const { text: _text, ...a } = seg;
  void _text;
  const ka = Object.keys(a);
  return ka.length === Object.keys(style).length && ka.every((k) => a[k as keyof Style] === style[k as keyof Style]);
}

/** Splits a line into styled segments, keeping SGR styling and dropping every other escape. */
export function parseAnsi(line: string): AnsiSegment[] {
  if (!line.includes("\u001b")) return [{ text: line }];
  const out: AnsiSegment[] = [];
  let style: Style = {};
  let last = 0;
  const push = (text: string) => {
    if (!text) return;
    const prev = out.at(-1);
    if (prev && sameStyle(prev, style)) prev.text += text;
    else out.push({ text, ...style });
  };
  for (const m of line.matchAll(ESCAPES)) {
    push(line.slice(last, m.index));
    last = m.index + m[0].length;
    if (m[2] === "m" && !m[1]?.includes("?")) {
      const params = m[1] ? m[1].split(";").map((x) => (x === "" ? 0 : Number(x))) : [0];
      style = apply(style, params);
    }
  }
  push(line.slice(last));
  return out.length ? out : [{ text: "" }];
}

export function stripAnsi(line: string): string {
  return line.includes("\u001b") ? line.replace(ESCAPES, "") : line;
}
