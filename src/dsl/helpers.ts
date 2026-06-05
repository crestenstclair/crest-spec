import type {
  CommandDescriptor,
  EventDescriptor,
  OperationDescriptor,
  InvariantDescriptor,
  ContextRelationship,
  LayerRule,
  Meta,
} from "../types.js";

export function command(name: string, payload?: Record<string, string>): CommandDescriptor {
  return { name, payload: payload ?? {} };
}

export function event(name: string, payload?: Record<string, string>): EventDescriptor {
  return { name, payload: payload ?? {} };
}

export function operation(
  name: string,
  config: { input: Record<string, string> },
): OperationDescriptor {
  return { name, input: config.input };
}

export function invariant(
  text: string,
  config?: { meta?: Meta },
): InvariantDescriptor {
  const desc: InvariantDescriptor = { text };
  if (config?.meta) desc.meta = config.meta;
  return desc;
}

export function relationship(
  from: string,
  to: string,
  config: {
    kind: ContextRelationship["kind"];
    direction?: ContextRelationship["direction"];
  },
): ContextRelationship {
  const rel: ContextRelationship = { from, to, kind: config.kind };
  if (config.direction) rel.direction = config.direction;
  return rel;
}

export function layer(name: string): { dependsOn: (deps: string[]) => LayerRule } {
  return {
    dependsOn(deps: string[]): LayerRule {
      return { layer: name, dependsOn: deps };
    },
  };
}
