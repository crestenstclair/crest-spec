import { describe, test, expect } from "bun:test";
import { WaveVerifier } from "../../src/engine/wave-verifier";

describe("WaveVerifier", () => {
  const verifier = new WaveVerifier();

  test("passes when command exits 0", async () => {
    const fileMap = new Map([["src/Comp/Phrase/Phrase.cs", "agg.Comp.Phrase"]]);
    const result = await verifier.verify(fileMap, ["true"], "/tmp");
    expect(result.passed).toBe(true);
    expect(result.errors).toHaveLength(0);
  });

  test("fails when command exits non-zero", async () => {
    const fileMap = new Map([["src/Comp/Phrase/Phrase.cs", "agg.Comp.Phrase"]]);
    const result = await verifier.verify(fileMap, ["false"], "/tmp");
    expect(result.passed).toBe(false);
  });

  test("attributes errors to resources by file path", async () => {
    const fileMap = new Map([
      ["src/Comp/Phrase/Phrase.cs", "agg.Comp.Phrase"],
      ["src/Comp/Chain/Chain.cs", "agg.Comp.Chain"],
    ]);
    const script = `echo "src/Comp/Phrase/Phrase.cs(10,5): error CS1002: ; expected" >&2; exit 1`;
    const result = await verifier.verify(fileMap, ["bash", "-c", script], "/tmp");

    expect(result.passed).toBe(false);
    expect(result.errors.length).toBeGreaterThanOrEqual(1);
    const phraseErrors = result.errors.filter((e) => e.resourceId === "agg.Comp.Phrase");
    expect(phraseErrors.length).toBeGreaterThanOrEqual(1);
  });

  test("unattributable errors get __unknown__ resource id", async () => {
    const fileMap = new Map([["src/Comp/Phrase/Phrase.cs", "agg.Comp.Phrase"]]);
    const script = `echo "some/other/file.cs(1,1): error CS9999: bad" >&2; exit 1`;
    const result = await verifier.verify(fileMap, ["bash", "-c", script], "/tmp");

    expect(result.passed).toBe(false);
    const unknowns = result.errors.filter((e) => e.resourceId === "__unknown__");
    expect(unknowns.length).toBeGreaterThanOrEqual(1);
  });

  test("handles command not found gracefully", async () => {
    const fileMap = new Map<string, string>();
    const result = await verifier.verify(
      fileMap,
      ["__nonexistent_command_12345__"],
      "/tmp",
    );
    expect(result.passed).toBe(false);
    expect(result.errors).toHaveLength(1);
  });

  test("deduplicates identical error lines", async () => {
    const fileMap = new Map([["src/A.cs", "res.A"]]);
    const script = `echo "src/A.cs(1): error E1: dup" >&2; echo "src/A.cs(1): error E1: dup" >&2; exit 1`;
    const result = await verifier.verify(fileMap, ["bash", "-c", script], "/tmp");

    expect(result.passed).toBe(false);
    const aErrors = result.errors.filter((e) => e.resourceId === "res.A");
    expect(aErrors).toHaveLength(1);
  });
});
