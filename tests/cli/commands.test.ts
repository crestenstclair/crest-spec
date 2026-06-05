import { describe, test, expect } from "bun:test";
import { mkdtemp, rm } from "fs/promises";
import { join } from "path";
import { tmpdir } from "os";
import { existsSync } from "fs";

describe("init command", () => {
  test("creates crest-spec.ts and crest-spec.db", async () => {
    const dir = await mkdtemp(join(tmpdir(), "crest-init-"));
    try {
      const { initCommand } = await import("../../src/cli/commands/init");
      const code = await initCommand(dir);
      expect(code).toBe(0);
      expect(existsSync(join(dir, "crest-spec.ts"))).toBe(true);
      expect(existsSync(join(dir, "crest-spec.db"))).toBe(true);
    } finally {
      await rm(dir, { recursive: true });
    }
  });

  test("refuses to overwrite existing spec", async () => {
    const dir = await mkdtemp(join(tmpdir(), "crest-init-"));
    try {
      await Bun.write(join(dir, "crest-spec.ts"), "existing");
      const { initCommand } = await import("../../src/cli/commands/init");
      const code = await initCommand(dir);
      expect(code).toBe(1);
    } finally {
      await rm(dir, { recursive: true });
    }
  });
});
