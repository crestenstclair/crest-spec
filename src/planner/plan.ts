export interface PlannedAction {
  resourceId: string;
  action: "create" | "modify" | "destroy" | "refresh";
  reason: string;
  affectedFiles: string[];
  cascadedFrom?: string;
}

export interface InvariantViolation {
  invariant: string;
  resourceId: string | null;
  detail: string;
}

export class Plan {
  constructor(
    readonly actions: PlannedAction[],
    readonly invariantViolations: InvariantViolation[],
  ) {}

  get isEmpty(): boolean {
    return this.actions.length === 0 && this.invariantViolations.length === 0;
  }

  display(): string {
    if (this.isEmpty) return "No changes detected.";

    const lines: string[] = [];

    const creates = this.actions.filter((a) => a.action === "create");
    const modifies = this.actions.filter((a) => a.action === "modify");
    const destroys = this.actions.filter((a) => a.action === "destroy");
    const refreshes = this.actions.filter((a) => a.action === "refresh");

    for (const action of [...creates, ...modifies, ...destroys, ...refreshes]) {
      const prefix =
        action.action === "create" ? "+" :
        action.action === "modify" ? "~" :
        action.action === "destroy" ? "-" : "?";

      lines.push(`${prefix} ${action.resourceId}`);
      lines.push(`  reason: ${action.reason}`);
      if (action.cascadedFrom) {
        lines.push(`  cascaded from: ${action.cascadedFrom}`);
      }
      if (action.affectedFiles.length > 0) {
        lines.push(`  files: ${action.affectedFiles.join(", ")}`);
      }
      lines.push("");
    }

    if (this.invariantViolations.length > 0) {
      lines.push("Invariant violations:");
      for (const v of this.invariantViolations) {
        lines.push(`  ! ${v.invariant}`);
        if (v.resourceId) lines.push(`    resource: ${v.resourceId}`);
        lines.push(`    detail: ${v.detail}`);
      }
    }

    return lines.join("\n");
  }
}
