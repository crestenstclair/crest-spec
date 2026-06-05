export interface WaveError {
  resourceId: string;
  filePath: string;
  errorText: string;
}

export interface WaveVerificationResult {
  passed: boolean;
  errors: WaveError[];
  rawOutput: string;
}

export interface IWaveVerifier {
  verify(
    fileToResource: Map<string, string>,
    command: string[],
    projectRoot: string,
  ): Promise<WaveVerificationResult>;
}

export class WaveVerifier implements IWaveVerifier {
  async verify(
    fileToResource: Map<string, string>,
    command: string[],
    projectRoot: string,
  ): Promise<WaveVerificationResult> {
    try {
      const proc = Bun.spawn(command, {
        cwd: projectRoot,
        stdout: "pipe",
        stderr: "pipe",
      });

      const [stdout, stderr] = await Promise.all([
        new Response(proc.stdout).text(),
        new Response(proc.stderr).text(),
      ]);
      const exitCode = await proc.exited;
      const rawOutput = (stdout + "\n" + stderr).trim();

      if (exitCode === 0) {
        return { passed: true, errors: [], rawOutput };
      }

      const errors = this.attributeErrors(rawOutput, fileToResource);
      return { passed: false, errors, rawOutput };
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      return {
        passed: false,
        errors: [{ resourceId: "__unknown__", filePath: "", errorText: msg }],
        rawOutput: msg,
      };
    }
  }

  private attributeErrors(
    output: string,
    fileToResource: Map<string, string>,
  ): WaveError[] {
    const lines = output.split("\n");
    const errors: WaveError[] = [];
    const seen = new Set<string>();

    const matchFile = (text: string): { filePath: string; resourceId: string } | null => {
      for (const [filePath, resourceId] of fileToResource) {
        if (text.includes(filePath)) {
          return { filePath, resourceId };
        }
      }
      return null;
    };

    const WINDOW = 10;
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (!line.includes("error") && !line.includes("Error") && !line.includes("fail")) continue;

      let match = matchFile(line);
      if (!match) {
        for (let j = 1; j <= WINDOW && !match; j++) {
          if (i + j < lines.length) match = matchFile(lines[i + j]);
          if (!match && i - j >= 0) match = matchFile(lines[i - j]);
        }
      }

      const resourceId = match?.resourceId ?? "__unknown__";
      const filePath = match?.filePath ?? "";
      const key = `${resourceId}:${line.trim()}`;
      if (!seen.has(key)) {
        seen.add(key);
        errors.push({ resourceId, filePath, errorText: line.trim() });
      }
    }

    return errors;
  }
}
