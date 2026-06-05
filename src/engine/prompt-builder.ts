import type { ResourceDescriptor, Meta } from "../types.js";
import type { IResourceRegistry } from "../registry/resource-registry.js";

export interface PromptConfig {
  language: string;
  framework?: string;
  style?: string;
  avoid?: string[];
  rules?: string[];
}

export interface IPromptBuilder {
  build(resource: ResourceDescriptor, registry: IResourceRegistry): string;
  systemPrompt(): string;
  readonly language: string;
}

export class PromptBuilder implements IPromptBuilder {
  constructor(private readonly config: PromptConfig) {}

  static fromMeta(meta: Meta): PromptBuilder {
    return new PromptBuilder({
      language: (meta.language as string) ?? "csharp",
      framework: meta.framework as string | undefined,
      style: meta.style as string | undefined,
      avoid: meta.avoid as string[] | undefined,
      rules: meta.rules as string[] | undefined,
    });
  }

  get language(): string {
    return this.config.language;
  }

  systemPrompt(): string {
    const lang = this.config.language;
    const framework = this.config.framework;
    const lines: string[] = [];

    const role = framework
      ? `You are a ${lang} code generator for a ${framework} project following strict SOLID principles.`
      : `You are a ${lang} code generator following strict SOLID principles.`;
    lines.push(role);

    lines.push("");
    lines.push("## Output format");
    lines.push("For each file, output a fenced code block with a path annotation on the first line.");
    lines.push("Use `// path:` for languages with C-style comments, or `# path:` for Makefile, TOML, YAML, etc.");
    lines.push(`\`\`\`${lang}`);
    lines.push("// path: src/ContextName/FileName.ext");
    lines.push("```");

    lines.push("");
    lines.push("## Folder structure");
    lines.push("Group files by resource within the bounded context: `src/{ContextName}/{ResourceName}/`.");
    lines.push("Do NOT use architectural layer sub-folders like Domain/, Application/, Infrastructure/.");
    lines.push("Tests mirror the same structure: `tests/{ContextName}/{ResourceName}/`.");
    lines.push("Example: `src/Composition/Phrase/Phrase.cs`, `src/Composition/Phrase/IPhraseRepository.cs`, `tests/Composition/Phrase/PhraseTests.cs`");

    lines.push("");
    lines.push("## SOLID principles (mandatory)");
    lines.push("- **Dependency Injection**: Classes NEVER instantiate their own dependencies. Accept all collaborators via constructor.");
    lines.push("  Every class gets a full constructor accepting all dependencies, and optionally a convenience parameterless constructor that calls it with defaults.");
    lines.push("- **Single Responsibility**: One class, one reason to change. No god classes.");
    lines.push("- **Dependency Inversion**: Depend on interfaces/abstractions, not concretions. Any class another class depends on MUST have a corresponding interface.");
    lines.push("- **Interface Segregation**: Small focused interfaces. Don't force implementors to depend on methods they don't use.");
    lines.push("- **Open/Closed**: New behavior via new classes, not modifying existing ones.");

    if (this.config.style) {
      lines.push("");
      lines.push("## Code style");
      lines.push(this.config.style);
    }

    if (this.config.avoid && this.config.avoid.length > 0) {
      lines.push("");
      lines.push("## Avoid");
      for (const item of this.config.avoid) {
        lines.push(`- ${item}`);
      }
    }

    if (this.config.rules && this.config.rules.length > 0) {
      lines.push("");
      lines.push("## Additional rules");
      for (const rule of this.config.rules) {
        lines.push(`- ${rule}`);
      }
    }

    lines.push("");
    lines.push("## Shared module files");
    lines.push("Do NOT generate top-level `lib.rs` or context-level `mod.rs` files (e.g. `src/lib.rs`, `src/Kernel/mod.rs`).");
    lines.push("Those files are managed separately as assets. Only generate `mod.rs` files within your own resource subdirectory.");

    lines.push("");
    lines.push("## Required output");
    lines.push("Generate BOTH:");
    lines.push("1. The implementation file(s)");
    lines.push("2. A unit test file that tests the resource through its public API using injected mocks/stubs.");
    lines.push("");
    lines.push("Do not add any commentary outside of code blocks.");

    return lines.join("\n");
  }

  build(resource: ResourceDescriptor, registry: IResourceRegistry): string {
    if (resource.kind === "asset") {
      return this.buildAssetPrompt(resource, registry);
    }

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

  private buildAssetPrompt(resource: ResourceDescriptor, registry: IResourceRegistry): string {
    const sections: string[] = [];
    const assetKindName = resource.declaration.assetKind as string;

    sections.push(`## Asset: "${resource.name}" (${resource.id})`);
    sections.push(`Context: ${resource.context ?? "project-level"}`);
    sections.push(`Asset Kind: ${assetKindName}`);

    const kindResource = registry.getById(`assetKind.${assetKindName}`);
    if (kindResource) {
      sections.push("\n## Asset Kind Definition");
      if (kindResource.declaration.description) {
        sections.push(kindResource.declaration.description as string);
      }
      if (kindResource.declaration.filePattern) {
        sections.push(`\nFile pattern: ${kindResource.declaration.filePattern}`);
      }
      if (kindResource.meta.prompts && (kindResource.meta.prompts as string[]).length > 0) {
        sections.push("\n### Kind Prompts");
        for (const p of kindResource.meta.prompts as string[]) {
          sections.push(`- ${p}`);
        }
      }
      if (kindResource.meta.references && (kindResource.meta.references as string[]).length > 0) {
        sections.push("\n### Kind References");
        for (const ref of kindResource.meta.references as string[]) {
          sections.push(`- ${ref}`);
        }
      }
    }

    sections.push("\n## Asset Description");
    if (resource.declaration.description) {
      sections.push(resource.declaration.description as string);
    }

    if (resource.meta.prompts && (resource.meta.prompts as string[]).length > 0) {
      sections.push("\n### Asset Prompts");
      for (const p of resource.meta.prompts as string[]) {
        sections.push(`- ${p}`);
      }
    }

    if (resource.meta.references && (resource.meta.references as string[]).length > 0) {
      sections.push("\n### Asset References");
      for (const ref of resource.meta.references as string[]) {
        sections.push(`- ${ref}`);
      }
    }

    const targets = resource.dependencies.filter(
      (d) => d.kind === "uses" && !d.targetId.startsWith("assetKind."),
    );
    if (targets.length > 0) {
      sections.push("\n## Target Resources");
      for (const dep of targets) {
        const target = registry.getById(dep.targetId);
        if (!target) continue;
        sections.push(`\n### ${target.kind}: ${target.name} (${target.id})`);
        sections.push("```json");
        sections.push(JSON.stringify(target.declaration, null, 2));
        sections.push("```");
        if (target.commands && target.commands.length > 0) {
          sections.push("Commands:");
          for (const cmd of target.commands) {
            sections.push(`- **${cmd.name}**: ${JSON.stringify(cmd.payload)}`);
          }
        }
        if (target.events && target.events.length > 0) {
          sections.push("Events:");
          for (const evt of target.events) {
            sections.push(`- **${evt.name}**: ${JSON.stringify(evt.payload)}`);
          }
        }
        if (target.invariants && target.invariants.length > 0) {
          sections.push("Invariants:");
          for (const inv of target.invariants) {
            sections.push(`- ${inv}`);
          }
        }
      }
    }

    return sections.join("\n");
  }
}
