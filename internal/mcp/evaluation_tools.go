package mcp

import (
	"context"
	"encoding/json"
	"time"
)

func (s *Server) registerEvaluationTools() {
	s.addTool(toolDef{
		Name: "spec/evaluation_cases", Description: "List immutable historical, curated, or imported evaluation cases, or inspect one case by id.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"limit":{"type":"integer"}}}`),
	}, specTool("evaluation cases", func(ctx context.Context, a specEvaluationInspectArgs) (any, error) {
		if a.ID != "" {
			return s.spec.GetEvaluationCase(ctx, a.ID)
		}
		return s.spec.ListEvaluationCases(ctx, a.Limit)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_case_from_attempt", Description: "Assess an ordinary terminal attempt and create a reproducible historical evaluation case when its immutable context/execution/candidate/validation chain is complete.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"attempt_id":{"type":"string"},"project_name":{"type":"string"}},"required":["attempt_id","project_name"]}`),
	}, specTool("evaluation case from attempt", func(ctx context.Context, a specEvaluationHistoricalCaseArgs) (any, error) {
		return s.spec.CreateHistoricalEvaluationCase(ctx, a.AttemptID, a.ProjectName)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_case_curate", Description: "Create an immutable curated evaluation case in SQLite. Payload and expected outcome are content-addressed and deduplicated.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"case":{"type":"object","properties":{"provenance":{"type":"string","enum":["curated","imported"]},"project_name":{"type":"string"},"goal_id":{"type":"string"},"capability_id":{"type":"string"},"resource_id":{"type":"string"},"spec_hash":{"type":"string"},"repository_hash":{"type":"string"},"resource_declaration_hash":{"type":"string"},"plan_operation_id":{"type":"string"},"payload":{"type":"object"},"expected_outcome":{"type":"object"}},"required":["project_name","resource_id","spec_hash","repository_hash","resource_declaration_hash","payload","expected_outcome"]}},"required":["case"]}`),
	}, specTool("evaluation case curate", func(ctx context.Context, a specEvaluationCuratedCaseArgs) (any, error) {
		return s.spec.CreateCuratedEvaluationCase(ctx, a.Case)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_datasets", Description: "List evaluation datasets or inspect one immutable case membership and split assignment by id.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"limit":{"type":"integer"}}}`),
	}, specTool("evaluation datasets", func(ctx context.Context, a specEvaluationInspectArgs) (any, error) {
		if a.ID != "" {
			return s.spec.GetEvaluationDataset(ctx, a.ID)
		}
		return s.spec.ListEvaluationDatasets(ctx, a.Limit)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_dataset_create", Description: "Create a draft SQLite-backed evaluation dataset.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"description":{"type":"string"}},"required":["name"]}`),
	}, specTool("evaluation dataset create", func(ctx context.Context, a specEvaluationDatasetCreateArgs) (any, error) {
		return s.spec.CreateEvaluationDataset(ctx, a.Name, a.Description)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_dataset_add_case", Description: "Add one immutable case to a draft dataset with a training, development, or held_out split.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"dataset_id":{"type":"string"},"case_id":{"type":"string"},"split":{"type":"string","enum":["training","development","held_out"]}},"required":["dataset_id","case_id","split"]}`),
	}, specToolErr("evaluation dataset add case", map[string]bool{"added": true}, func(ctx context.Context, a specEvaluationDatasetCaseArgs) error {
		return s.spec.AddEvaluationDatasetCase(ctx, a.DatasetID, a.CaseID, a.Split)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_dataset_seal", Description: "Seal a dataset into an immutable identity before creating runs.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"dataset_id":{"type":"string"}},"required":["dataset_id"]}`),
	}, specTool("evaluation dataset seal", func(ctx context.Context, a specEvaluationDatasetSealArgs) (any, error) {
		return s.spec.SealEvaluationDataset(ctx, a.DatasetID)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_configurations", Description: "List immutable evaluation configurations or inspect one by id.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"limit":{"type":"integer"}}}`),
	}, specTool("evaluation configurations", func(ctx context.Context, a specEvaluationInspectArgs) (any, error) {
		if a.ID != "" {
			return s.spec.GetEvaluationConfiguration(ctx, a.ID)
		}
		return s.spec.ListEvaluationConfigurations(ctx, a.Limit)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_configuration_create", Description: "Create or reuse an immutable redacted planner/context/template/role/host/model configuration.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"configuration":{"type":"object"}},"required":["configuration"]}`),
	}, specTool("evaluation configuration create", func(ctx context.Context, a specEvaluationConfigurationArgs) (any, error) {
		return s.spec.CreateEvaluationConfiguration(ctx, a.Configuration)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_runs", Description: "List evaluation runs or inspect assignments, aggregates, comparisons, conclusion, and provenance for one run.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"limit":{"type":"integer"}}}`),
	}, specTool("evaluation runs", func(ctx context.Context, a specEvaluationInspectArgs) (any, error) {
		if a.ID != "" {
			return s.spec.GetEvaluationRun(ctx, a.ID)
		}
		return s.spec.ListEvaluationRuns(ctx, a.Limit)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_run_create", Description: "Create a host-driven evaluation run over a sealed dataset and immutable baseline/candidate configurations.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"dataset_id":{"type":"string"},"name":{"type":"string"},"variants":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"configuration_id":{"type":"string"},"baseline":{"type":"boolean"}},"required":["name","configuration_id"]}},"metric_policy":{"type":"object"},"minimum_sample_size":{"type":"integer"},"practical_significance":{"type":"number"},"require_held_out":{"type":"boolean"}},"required":["dataset_id","name","variants"]}`),
	}, specTool("evaluation run create", func(ctx context.Context, a specEvaluationRunCreateArgs) (any, error) {
		return s.spec.CreateEvaluationRun(ctx, a.DatasetID, a.Name, a.Variants, a.MetricPolicy, a.MinimumSampleSize, a.PracticalSignificance, a.RequireHeldOut)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_assignment_claim", Description: "Claim one pending case/variant assignment with a renewable token-bound lease. Held-out expected outcomes are withheld.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"run_id":{"type":"string"},"owner":{"type":"string"},"split":{"type":"string","enum":["","training","development","held_out"]},"lease_seconds":{"type":"integer"}},"required":["run_id","owner"]}`),
	}, specTool("evaluation assignment claim", func(ctx context.Context, a specEvaluationClaimArgs) (any, error) {
		return s.spec.ClaimEvaluationAssignment(ctx, a.RunID, a.Owner, a.Split, time.Duration(a.LeaseSeconds)*time.Second)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_assignment_heartbeat", Description: "Renew an active evaluation assignment lease.",
		InputSchema: evaluationLeaseSchema(true),
	}, specTool("evaluation assignment heartbeat", func(ctx context.Context, a specEvaluationLeaseArgs) (any, error) {
		expires, err := s.spec.HeartbeatEvaluationAssignment(ctx, a.AssignmentID, a.LeaseID, a.Owner, a.LeaseToken, time.Duration(a.Seconds)*time.Second)
		if err != nil {
			return nil, err
		}
		return map[string]any{"lease_expires_at": expires}, nil
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_assignment_release", Description: "Release an active evaluation assignment so another host can claim it.",
		InputSchema: evaluationLeaseSchema(false),
	}, specToolErr("evaluation assignment release", map[string]bool{"released": true}, func(ctx context.Context, a specEvaluationLeaseArgs) error {
		return s.spec.ReleaseEvaluationAssignment(ctx, a.AssignmentID, a.LeaseID, a.Owner, a.LeaseToken)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_assignment_submit", Description: "Submit one authoritative terminal result and metric observations for a leased assignment. Ordinary attempt linkage allows engine-derived metrics to override host claims.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"assignment_id":{"type":"string"},"lease_id":{"type":"string"},"owner":{"type":"string"},"lease_token":{"type":"string"},"result":{"type":"object","properties":{"attempt_id":{"type":"string"},"terminal_status":{"type":"string"},"reason":{"type":"string"},"metrics":{"type":"array","items":{"type":"object"}}},"required":["terminal_status"]}},"required":["assignment_id","lease_id","owner","lease_token","result"]}`),
	}, specTool("evaluation assignment submit", func(ctx context.Context, a specEvaluationSubmitArgs) (any, error) {
		return s.spec.SubmitEvaluationAssignment(ctx, a.AssignmentID, a.LeaseID, a.Owner, a.LeaseToken, a.Result)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_run_finalize", Description: "Persist deterministic aggregates and baseline comparisons, producing a winner or an explicit inconclusive conclusion.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"run_id":{"type":"string"}},"required":["run_id"]}`),
	}, specTool("evaluation run finalize", func(ctx context.Context, a specEvaluationRunArgs) (any, error) {
		return s.spec.FinalizeEvaluationRun(ctx, a.RunID)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_promotions", Description: "List evidence-gated promotion proposals or inspect the exact change, rollback identity, evaluation configuration, and human decision history for one proposal.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"status":{"type":"string"},"limit":{"type":"integer"}}}`),
	}, specTool("evaluation promotions", func(ctx context.Context, a specEvaluationPromotionListArgs) (any, error) {
		if a.ID != "" {
			return s.spec.GetEvaluationPromotion(ctx, a.ID)
		}
		return s.spec.ListEvaluationPromotions(ctx, a.Status, a.Limit)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_learning_propose", Description: "Create an exact learning-template promotion proposal bound to a qualifying evaluation winner and rollback identity.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"lang":{"type":"string"},"min_confidence":{"type":"number"},"min_times_applied":{"type":"integer"},"template_path":{"type":"string"},"run_id":{"type":"string"},"variant_name":{"type":"string"}},"required":["run_id","variant_name"]}`),
	}, specTool("evaluation learning propose", func(ctx context.Context, a specEvaluationLearningProposalArgs) (any, error) {
		return s.spec.ProposeLearningPromotion(ctx, a.Lang, a.MinConfidence, a.MinTimesApplied, a.TemplatePath, a.RunID, a.VariantName)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_promotion_decide", Description: "Record an immutable human approval or rejection for an evaluation promotion proposal.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"proposal_id":{"type":"string"},"decision":{"type":"string","enum":["approved","rejected"]},"actor":{"type":"string"},"reason":{"type":"string"}},"required":["proposal_id","decision","actor","reason"]}`),
	}, specTool("evaluation promotion decide", func(ctx context.Context, a specEvaluationPromotionDecisionArgs) (any, error) {
		return s.spec.DecideLearningPromotion(ctx, a.ProposalID, a.Decision, a.Actor, a.Reason)
	}))

	s.addTool(toolDef{
		Name: "spec/evaluation_promotion_apply", Description: "Apply an approved exact learning proposal if the target still matches its rollback identity, then persist the applied decision.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"proposal_id":{"type":"string"},"actor":{"type":"string"},"reason":{"type":"string"}},"required":["proposal_id","actor","reason"]}`),
	}, specTool("evaluation promotion apply", func(ctx context.Context, a specEvaluationPromotionApplyArgs) (any, error) {
		return s.spec.ApplyLearningPromotion(ctx, a.ProposalID, a.Actor, a.Reason)
	}))
}

func evaluationLeaseSchema(includeSeconds bool) json.RawMessage {
	seconds := ""
	if includeSeconds {
		seconds = `,"seconds":{"type":"integer"}`
	}
	return json.RawMessage(`{"type":"object","properties":{"assignment_id":{"type":"string"},"lease_id":{"type":"string"},"owner":{"type":"string"},"lease_token":{"type":"string"}` + seconds + `},"required":["assignment_id","lease_id","owner","lease_token"]}`)
}
