import type { ResourceDescriptor } from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";

export interface IPromptBuilder {
  build(resource: ResourceDescriptor, registry: IResourceRegistry): string;
  systemPrompt(): string;
}

export class PromptBuilder implements IPromptBuilder {
  systemPrompt(): string {
    return [
      "You are a TypeScript code generator. You produce implementation files for declared software resources.",
      "For each file you generate, output it as a fenced code block with a path annotation on the first line:",
      "```ts",
      "// path: src/example/file.ts",
      "// ... file content ...",
      "```",
      "Generate both the implementation file and a test file for each resource.",
      "Use pure functions and immutable data structures unless the declaration specifies otherwise.",
      "All generated code must compile with strict TypeScript.",
      "Do not add any commentary outside of code blocks.",
    ].join("\n");
  }

  build(resource: ResourceDescriptor, registry: IResourceRegistry): string {
    const sections: string[] = [];

    sections.push(`## Resource: ${resource.kind} "${resource.name}" (${resource.id})`);
    sections.push(`Context: ${resource.context ?? "project-level"}`);
    sections.push(`Layer: ${resource.layer ?? "unspecified"}`);

    sections.push("\n## Declaration");
    sections.push("```json");
    sections.push(JSON.stringify(resource.declaration, null, 2));
    sections.push("```");

    if (resource.commands && resource.commands.length > 0) {
      sections.push("\n## Commands");
      for (const cmd of resource.commands) {
        sections.push(`- **${cmd.name}**: ${JSON.stringify(cmd.payload)}`);
      }
    }

    if (resource.events && resource.events.length > 0) {
      sections.push("\n## Events");
      for (const evt of resource.events) {
        sections.push(`- **${evt.name}**: ${JSON.stringify(evt.payload)}`);
      }
    }

    if (resource.invariants && resource.invariants.length > 0) {
      sections.push("\n## Invariants");
      for (const inv of resource.invariants) {
        sections.push(`- ${inv}`);
      }
    }

    if (Object.keys(resource.meta).length > 0) {
      sections.push("\n## Meta");
      sections.push("```json");
      sections.push(JSON.stringify(resource.meta, null, 2));
      sections.push("```");
    }

    const implementedPorts = resource.dependencies.filter((d) => d.kind === "implements");
    for (const dep of implementedPorts) {
      const port = registry.getById(dep.targetId);
      if (port) {
        sections.push("\n## Implements Port: " + port.name);
        sections.push("Contract:");
        sections.push("```json");
        sections.push(JSON.stringify(port.declaration.contract, null, 2));
        sections.push("```");
      }
    }

    const usedDeps = resource.dependencies.filter((d) => d.kind === "uses");
    if (usedDeps.length > 0) {
      sections.push("\n## Dependencies (uses)");
      for (const dep of usedDeps) {
        const target = registry.getById(dep.targetId);
        if (target) {
          sections.push(`- ${target.name} (${target.id}): ${JSON.stringify(target.declaration)}`);
        }
      }
    }

    return sections.join("\n");
  }
}
