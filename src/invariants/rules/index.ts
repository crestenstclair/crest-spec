export { AggregateHasRepository } from "./aggregate-has-repository.js";
export { ContextBoundaries } from "./context-boundaries.js";
export { DependencyRule } from "./dependency-rule.js";
export { MutationsThroughServices } from "./mutations-through-services.js";
export { DomainNoInfraImports } from "./domain-no-infra-imports.js";
export { ContractCompliance } from "./contract-compliance.js";

import { AggregateHasRepository } from "./aggregate-has-repository.js";
import { ContextBoundaries } from "./context-boundaries.js";
import { DependencyRule } from "./dependency-rule.js";
import { MutationsThroughServices } from "./mutations-through-services.js";
import { DomainNoInfraImports } from "./domain-no-infra-imports.js";
import { ContractCompliance } from "./contract-compliance.js";
import type { IInvariantRule } from "../invariant-checker.js";

export function allRules(): IInvariantRule[] {
  return [
    new AggregateHasRepository(),
    new ContextBoundaries(),
    new DependencyRule(),
    new MutationsThroughServices(),
    new DomainNoInfraImports(),
    new ContractCompliance(),
  ];
}
