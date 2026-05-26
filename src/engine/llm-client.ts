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

      const env = { ...process.env };
      delete env.CLAUDE_CODE_OAUTH_TOKEN;
      delete env.ANTHROPIC_AUTH_TOKEN;
      delete env.ANTHROPIC_API_KEY;
      const proc = Bun.spawn(["claude", "-p", "--model", this.modelId, "--output-format", "stream-json", "--verbose"], {
        stdin: new TextEncoder().encode(fullPrompt),
        stdout: "pipe",
        stderr: "inherit",
        env,
      });

      const chunks: string[] = [];
      const reader = proc.stdout.getReader();
      const decoder = new TextDecoder();
      let totalChars = 0;
      let lastDot = 0;

      process.stdout.write("      Streaming: ");
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        const text = decoder.decode(value);
        chunks.push(text);

        for (const line of text.split("\n")) {
          if (!line.trim()) continue;
          try {
            const event = JSON.parse(line);
            if (event.type === "content_block_delta" && event.delta?.text) {
              totalChars += event.delta.text.length;
            } else if (event.type === "result" && event.result) {
              totalChars = event.result.length;
            }
          } catch {}
        }

        if (totalChars - lastDot > 500) {
          process.stdout.write(".");
          lastDot = totalChars;
        }
      }

      const exitCode = await proc.exited;
      const elapsed = ((performance.now() - start) / 1000).toFixed(1);
      const raw = chunks.join("");

      if (exitCode !== 0) {
        process.stdout.write(" FAILED\n");
        const errMsg = raw.slice(0, 200);
        console.log(`      claude CLI failed (exit ${exitCode}): ${errMsg}`);
        if (attempt < maxAttempts - 1) continue;
        throw new Error(`claude CLI exited with ${exitCode} after ${maxAttempts} attempts: ${errMsg}`);
      }

      let result = "";
      for (const line of raw.split("\n")) {
        if (!line.trim()) continue;
        try {
          const event = JSON.parse(line);
          if (event.type === "result") {
            result = event.result;
            break;
          }
        } catch {}
      }

      if (!result) result = raw;

      process.stdout.write(` done\n`);
      console.log(`      ${elapsed}s, ${result.length} chars output`);
      return result;
    }
    throw new Error("unreachable");
  }
}
