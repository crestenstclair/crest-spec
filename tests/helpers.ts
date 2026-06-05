import type { ResourceDescriptor, Meta } from "../src/types";

export function makeResource(overrides: Partial<ResourceDescriptor> = {}): ResourceDescriptor {
  return {
    id: "aggregate.TestContext.TestResource",
    kind: "aggregate",
    name: "TestResource",
    context: "TestContext",
    layer: null,
    declaration: {},
    meta: {},
    dependencies: [],
    ...overrides,
  };
}
