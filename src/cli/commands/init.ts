import { existsSync } from "fs";
import { join } from "path";
import { Formatter } from "../formatter.js";

export async function initCommand(projectDir: string): Promise<number> {
  const specPath = join(projectDir, "crest-spec.ts");
  const dbPath = join(projectDir, "crest-spec.db");

  if (existsSync(specPath)) {
    console.log(Formatter.error(`${specPath} already exists`));
    return 1;
  }

  const scaffold = `import { project, command, event, layer } from "crest-spec";

const app = project("my-project", {
  layers: ["domain", "application", "infrastructure"],
  rules: [
    layer("domain").dependsOn([]),
    layer("application").dependsOn(["domain"]),
    layer("infrastructure").dependsOn(["application", "domain"]),
  ],
});

// const myContext = app.context("MyContext", {
//   purpose: "describe your bounded context here",
// });

export default app;
`;

  await Bun.write(specPath, scaffold);
  console.log(Formatter.success(`Created ${specPath}`));

  const { StateDatabase } = await import("../../state/state-database.js");
  new StateDatabase(dbPath);
  console.log(Formatter.success(`Created ${dbPath}`));

  return 0;
}
