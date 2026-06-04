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
    const errors: WaveError[] = [];
    const seen = new Set<string>();

    for (const line of output.split("\n")) {
      if (!line.includes("error")) continue;

      let matched = false;
      for (const [filePath, resourceId] of fileToResource) {
        if (line.includes(filePath)) {
          const key = `${resourceId}:${line}`;
          if (!seen.has(key)) {
            seen.add(key);
            errors.push({ resourceId, filePath, errorText: line.trim() });
          }
          matched = true;
          break;
        }
      }

      if (!matched) {
        const key = `__unknown__:${line}`;
        if (!seen.has(key)) {
          seen.add(key);
          errors.push({ resourceId: "__unknown__", filePath: "", errorText: line.trim() });
        }
      }
    }

    return errors;
  }
}
