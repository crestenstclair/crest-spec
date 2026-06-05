import type { ResourceDescriptor, ContextRelationship, InvariantDescriptor } from "../types.js";

export interface IResourceRegistry {
  register(descriptor: ResourceDescriptor): void;
  getById(id: string): ResourceDescriptor | null;
  getAll(): ResourceDescriptor[];
  getByKind(kind: string): ResourceDescriptor[];
  getByContext(contextId: string): ResourceDescriptor[];
  getDependents(id: string): ResourceDescriptor[];
  getDependencies(id: string): ResourceDescriptor[];
  topologicalOrder(): ResourceDescriptor[];
  getContextMap(): ContextRelationship[];
  getInvariants(): InvariantDescriptor[];
  setContextMap(relationships: ContextRelationship[]): void;
  setInvariants(invariants: InvariantDescriptor[]): void;
}

export class ResourceRegistry implements IResourceRegistry {
  private resources = new Map<string, ResourceDescriptor>();
  private contextRelationships: ContextRelationship[] = [];
  private projectInvariants: InvariantDescriptor[] = [];

  register(descriptor: ResourceDescriptor): void {
    if (this.resources.has(descriptor.id)) {
      throw new Error(`Duplicate resource ID: ${descriptor.id}`);
    }
    this.resources.set(descriptor.id, descriptor);
  }

  getById(id: string): ResourceDescriptor | null {
    return this.resources.get(id) ?? null;
  }

  getAll(): ResourceDescriptor[] {
    return [...this.resources.values()];
  }

  getByKind(kind: string): ResourceDescriptor[] {
    return this.getAll().filter((r) => r.kind === kind);
  }

  getByContext(contextId: string): ResourceDescriptor[] {
    return this.getAll().filter((r) => r.context === contextId);
  }

  getDependents(id: string): ResourceDescriptor[] {
    return this.getAll().filter((r) =>
      r.dependencies.some((d) => d.targetId === id),
    );
  }

  getDependencies(id: string): ResourceDescriptor[] {
    const resource = this.getById(id);
    if (!resource) return [];
    return resource.dependencies
      .map((d) => this.getById(d.targetId))
      .filter((r): r is ResourceDescriptor => r !== null);
  }

  topologicalOrder(): ResourceDescriptor[] {
    const visited = new Set<string>();
    const result: ResourceDescriptor[] = [];

    const visit = (id: string) => {
      if (visited.has(id)) return;
      visited.add(id);
      const resource = this.getById(id);
      if (!resource) return;
      for (const dep of resource.dependencies) {
        visit(dep.targetId);
      }
      result.push(resource);
    };

    for (const resource of this.resources.values()) {
      visit(resource.id);
    }
    return result;
  }

  getContextMap(): ContextRelationship[] {
    return this.contextRelationships;
  }

  getInvariants(): InvariantDescriptor[] {
    return this.projectInvariants;
  }

  setContextMap(relationships: ContextRelationship[]): void {
    this.contextRelationships = relationships;
  }

  setInvariants(invariants: InvariantDescriptor[]): void {
    this.projectInvariants = invariants;
  }
}
