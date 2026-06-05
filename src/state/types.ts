export interface StoredResource {
  id: string;
  kind: string;
  context: string | null;
  declaration_hash: string;
  effective_hash: string;
  declaration_json: string;
  layer: string | null;
  settled_at: string | null;
  last_apply_id: number | null;
}

export interface StoredFile {
  path: string;
  resource_id: string;
  content_hash: string;
  generator: "deterministic" | "llm";
  model: string | null;
  prompt_hash: string | null;
  generated_at: string;
}

export interface StoredDependency {
  from_resource: string;
  to_resource: string;
  kind: string;
}

export interface StoredContextRelationship {
  from_context: string;
  to_context: string;
  kind: string;
  direction: string;
}

export interface ApplyRecord {
  id: number;
  started_at: string;
  finished_at: string | null;
  status: "running" | "ok" | "failed" | "aborted";
  spec_hash: string;
  notes: string | null;
}

export interface ActionRecord {
  apply_id: number;
  resource_id: string;
  action: "create" | "modify" | "destroy" | "noop";
  outcome: string;
}

export interface GenerationRecord {
  id?: number;
  apply_id: number;
  resource_id: string;
  model: string;
  prompt_hash: string;
  prompt_text: string;
  output_text: string;
  retries: number;
  outcome: "accepted" | "rejected";
  rejection_reason: string | null;
  created_at: string;
}

export interface InvariantCheckRecord {
  apply_id: number;
  invariant: string;
  resource_id: string | null;
  status: "ok" | "violated";
  detail: string | null;
}

export interface LockRecord {
  holder: string;
  acquired_at: string;
}

export interface AgentSessionRecord {
  apply_id: number;
  plan_json: string;
  waves_json: string;
  hashes_json: string;
  created_at: string;
}

export interface AgentNoteRecord {
  id?: number;
  resource_id: string;
  apply_id: number;
  content: string;
  created_at: string;
}
