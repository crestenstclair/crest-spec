export interface ILlmClient {
  generate(prompt: string, systemPrompt: string): Promise<string>;
  readonly modelId: string;
}

export class ClaudeCliClient implements ILlmClient {
  readonly modelId: string;

  constructor(modelId: string) {
    this.modelId = modelId;
  }

  async generate(prompt: string, systemPrompt: string): Promise<string> {
    const promptKb = Math.round(prompt.length / 1024);
    const fullPrompt = `${systemPrompt}\n\n${prompt}`;
    const maxAttempts = 3;

    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      if (attempt > 0) {
        console.log(`      Retrying claude CLI (attempt ${attempt + 1}/${maxAttempts})...`);
        await Bun.sleep(2000);
      }

      const start = performance.now();
      console.log(`      Sending to claude CLI (${promptKb}KB prompt)...`);

      const proc = Bun.spawn(["claude", "-p", "--model", this.modelId, "--disallowedTools", "Bash", "Read", "Edit", "Write", "Glob", "Grep", "WebFetch", "WebSearch"], {
        stdin: new TextEncoder().encode(fullPrompt),
        stdout: "pipe",
        stderr: "inherit",
      });

      const stdout = await new Response(proc.stdout).text();
      const exitCode = await proc.exited;
      const elapsed = ((performance.now() - start) / 1000).toFixed(1);

      if (exitCode !== 0) {
        const errMsg = stdout.slice(0, 300);
        console.log(`      claude CLI failed (exit ${exitCode}): ${errMsg}`);
        if (attempt < maxAttempts - 1) continue;
        throw new Error(`claude CLI exited with ${exitCode} after ${maxAttempts} attempts: ${errMsg}`);
      }

      console.log(`      ${elapsed}s, ${stdout.length} chars output`);
      return stdout;
    }
    throw new Error("unreachable");
  }
}
