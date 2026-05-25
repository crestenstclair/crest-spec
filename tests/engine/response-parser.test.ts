import { describe, test, expect } from "bun:test";
import { ResponseParser } from "../../src/engine/response-parser";

describe("ResponseParser", () => {
  const parser = new ResponseParser();

  test("parses a single fenced code block with path annotation", () => {
    const input = `Here is the implementation:

\`\`\`ts
// path: src/composition/domain/song.ts
export interface Song {
  id: string;
  name: string;
}
\`\`\``;

    const files = parser.parse(input);
    expect(files.size).toBe(1);
    expect(files.get("src/composition/domain/song.ts")).toContain("export interface Song");
  });

  test("parses multiple fenced code blocks", () => {
    const input = `
\`\`\`ts
// path: src/domain/song.ts
export interface Song { id: string; }
\`\`\`

\`\`\`ts
// path: src/domain/song.test.ts
import { Song } from "./song";
test("song exists", () => {});
\`\`\``;

    const files = parser.parse(input);
    expect(files.size).toBe(2);
    expect(files.has("src/domain/song.ts")).toBe(true);
    expect(files.has("src/domain/song.test.ts")).toBe(true);
  });

  test("strips the path comment from file content", () => {
    const input = `\`\`\`ts
// path: src/song.ts
export type Song = {};
\`\`\``;

    const files = parser.parse(input);
    const content = files.get("src/song.ts")!;
    expect(content).not.toContain("// path:");
    expect(content.trim()).toBe("export type Song = {};");
  });

  test("ignores code blocks without path annotations", () => {
    const input = `Here's an example:

\`\`\`ts
const x = 1;
\`\`\`

\`\`\`ts
// path: src/real.ts
export const y = 2;
\`\`\``;

    const files = parser.parse(input);
    expect(files.size).toBe(1);
    expect(files.has("src/real.ts")).toBe(true);
  });

  test("returns empty map for input with no code blocks", () => {
    const files = parser.parse("No code here.");
    expect(files.size).toBe(0);
  });
});
