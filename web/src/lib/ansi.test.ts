import { parseAnsi, stripAnsi } from "./ansi";

const ESC = "\u001b";

test("plain text is one segment", () => {
  expect(parseAnsi("hello")).toEqual([{ text: "hello" }]);
});

test("basic and bright foreground colours, bold and reset", () => {
  const segs = parseAnsi(`${ESC}[31mred${ESC}[0m plain ${ESC}[1;92mgreen${ESC}[22m!`);
  expect(segs).toEqual([
    { text: "red", fg: "red" },
    { text: " plain " },
    { text: "green", fg: "bright-green", bold: true },
    { text: "!", fg: "bright-green" },
  ]);
});

test("256 and truecolor", () => {
  expect(parseAnsi(`${ESC}[38;5;196mx`)[0]).toEqual({ text: "x", fgRgb: "rgb(255,0,0)" });
  expect(parseAnsi(`${ESC}[38;2;10;20;30;48;2;1;2;3mx`)[0]).toEqual({ text: "x", fgRgb: "rgb(10,20,30)", bgRgb: "rgb(1,2,3)" });
  expect(parseAnsi(`${ESC}[38;5;8mx`)[0]).toEqual({ text: "x", fg: "bright-black" });
});

test("background, italic, underline, dim and their resets", () => {
  expect(parseAnsi(`${ESC}[44;3;4;2mx${ESC}[23;24;49;39my`)).toEqual([
    { text: "x", bg: "blue", italic: true, underline: true, dim: true },
    { text: "y", dim: true },
  ]);
});

test("non-SGR escape sequences are dropped", () => {
  expect(parseAnsi(`a${ESC}[2Kb${ESC}]0;title\u0007c${ESC}[?25ld`)).toEqual([{ text: "abcd" }]);
});

test("a bare ESC[m resets", () => {
  expect(parseAnsi(`${ESC}[31ma${ESC}[mb`)).toEqual([{ text: "a", fg: "red" }, { text: "b" }]);
});

test("unterminated sequences do not swallow text", () => {
  expect(stripAnsi(`ok${ESC}[31`)).toBe("ok");
  expect(stripAnsi(`${ESC}[1mbold${ESC}[0m`)).toBe("bold");
});
