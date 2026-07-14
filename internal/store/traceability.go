package store

import (
	"context"
	"fmt"

	"github.com/crestenstclair/crest-spec/internal/db"
)

type IntentResourceTrace struct {
	ResourceID    string
	ResourceKind  string
	Contributions []IntentContribution
	Boundary      *IntentBoundaryProfile
	Asset         *IntentAssetProfile
}

type IntentContribution struct {
	CapabilityID string
	Description  string
}

type IntentBoundaryProfile struct {
	Direction, Kind, Method, Path, Protocol, Topology string
	Device, Medium, System, Topic, Trigger            string
	Surfaces, Accessibility                           []string
}

type IntentAssetProfile struct {
	Kind, Ecosystem, Witness, Source, SecretPolicy, FailurePolicy string
	Constraint, Audience, Predecessor, Compatibility, Rollback    string
	Signals, RequiredExamples                                     []string
}

type PersistedContribution struct {
	ProjectName, ResourceID, ResourceKind, CapabilityID, Description string
}

func reconcileResourceTrace(ctx context.Context, q *db.Queries, snapshot ProjectIntentSnapshot, timestamp string) error {
	if err := q.ClearBoundaryProfileItems(ctx, snapshot.ProjectName); err != nil {
		return fmt.Errorf("clear boundary profile items: %w", err)
	}
	if err := q.ClearAssetProfileItems(ctx, snapshot.ProjectName); err != nil {
		return fmt.Errorf("clear asset profile items: %w", err)
	}
	if err := q.DeactivateResourceContributions(ctx, db.DeactivateResourceContributionsParams{UpdatedAt: timestamp, ProjectName: snapshot.ProjectName}); err != nil {
		return fmt.Errorf("deactivate contributions: %w", err)
	}
	if err := q.DeactivateBoundaryProfiles(ctx, db.DeactivateBoundaryProfilesParams{UpdatedAt: timestamp, ProjectName: snapshot.ProjectName}); err != nil {
		return fmt.Errorf("deactivate boundary profiles: %w", err)
	}
	if err := q.DeactivateAssetProfiles(ctx, db.DeactivateAssetProfilesParams{UpdatedAt: timestamp, ProjectName: snapshot.ProjectName}); err != nil {
		return fmt.Errorf("deactivate asset profiles: %w", err)
	}

	for _, trace := range sortedByID(snapshot.ResourceTrace, func(v IntentResourceTrace) string { return v.ResourceID }) {
		for _, contribution := range sortedByID(trace.Contributions, func(v IntentContribution) string { return v.CapabilityID }) {
			err := q.UpsertResourceContribution(ctx, db.UpsertResourceContributionParams{
				ProjectName: snapshot.ProjectName, ResourceID: trace.ResourceID, ResourceKind: trace.ResourceKind,
				CapabilityID: contribution.CapabilityID, Contribution: contribution.Description,
				SpecHash: snapshot.SpecHash, UpdatedAt: timestamp,
			})
			if err != nil {
				return fmt.Errorf("upsert contribution %s -> %s: %w", trace.ResourceID, contribution.CapabilityID, err)
			}
		}
		if profile := trace.Boundary; profile != nil {
			err := q.UpsertBoundaryProfile(ctx, db.UpsertBoundaryProfileParams{
				ResourceID: trace.ResourceID, ProjectName: snapshot.ProjectName, ResourceKind: trace.ResourceKind,
				Direction: profile.Direction, ProfileKind: profile.Kind, Method: profile.Method, Path: profile.Path,
				Protocol: profile.Protocol, Topology: profile.Topology, Device: profile.Device, Medium: profile.Medium,
				SystemName: profile.System, Topic: profile.Topic, TriggerName: profile.Trigger,
				SpecHash: snapshot.SpecHash, UpdatedAt: timestamp,
			})
			if err != nil {
				return fmt.Errorf("upsert boundary profile %s: %w", trace.ResourceID, err)
			}
			if err := insertBoundaryItems(ctx, q, trace.ResourceID, "surface", profile.Surfaces); err != nil {
				return err
			}
			if err := insertBoundaryItems(ctx, q, trace.ResourceID, "accessibility", profile.Accessibility); err != nil {
				return err
			}
		}
		if profile := trace.Asset; profile != nil {
			err := q.UpsertAssetProfile(ctx, db.UpsertAssetProfileParams{
				ResourceID: trace.ResourceID, ProjectName: snapshot.ProjectName, ProfileKind: profile.Kind,
				Ecosystem: profile.Ecosystem, Witness: profile.Witness, Source: profile.Source,
				SecretPolicy: profile.SecretPolicy, FailurePolicy: profile.FailurePolicy, ConstraintText: profile.Constraint,
				Audience: profile.Audience, Predecessor: profile.Predecessor, Compatibility: profile.Compatibility,
				Rollback: profile.Rollback, SpecHash: snapshot.SpecHash, UpdatedAt: timestamp,
			})
			if err != nil {
				return fmt.Errorf("upsert asset profile %s: %w", trace.ResourceID, err)
			}
			if err := insertAssetItems(ctx, q, trace.ResourceID, "signal", profile.Signals); err != nil {
				return err
			}
			if err := insertAssetItems(ctx, q, trace.ResourceID, "required_example", profile.RequiredExamples); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertBoundaryItems(ctx context.Context, q *db.Queries, resourceID, kind string, values []string) error {
	for ordinal, value := range values {
		if err := q.InsertBoundaryProfileItem(ctx, db.InsertBoundaryProfileItemParams{ResourceID: resourceID, ItemKind: kind, Ordinal: int64(ordinal), Value: value}); err != nil {
			return fmt.Errorf("insert boundary profile item %s/%s: %w", resourceID, kind, err)
		}
	}
	return nil
}

func insertAssetItems(ctx context.Context, q *db.Queries, resourceID, kind string, values []string) error {
	for ordinal, value := range values {
		if err := q.InsertAssetProfileItem(ctx, db.InsertAssetProfileItemParams{ResourceID: resourceID, ItemKind: kind, Ordinal: int64(ordinal), Value: value}); err != nil {
			return fmt.Errorf("insert asset profile item %s/%s: %w", resourceID, kind, err)
		}
	}
	return nil
}

func (s *Store) ListResourceContributions(ctx context.Context, projectName string) ([]PersistedContribution, error) {
	rows, err := s.queries.ListResourceContributions(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list resource contributions: %w", err)
	}
	out := make([]PersistedContribution, 0, len(rows))
	for _, row := range rows {
		out = append(out, PersistedContribution{ProjectName: row.ProjectName, ResourceID: row.ResourceID, ResourceKind: row.ResourceKind, CapabilityID: row.CapabilityID, Description: row.Contribution})
	}
	return out, nil
}

func (s *Store) ListContributionsByResource(ctx context.Context, resourceID string) ([]PersistedContribution, error) {
	rows, err := s.queries.ListContributionsByResource(ctx, resourceID)
	if err != nil {
		return nil, fmt.Errorf("list contributions by resource: %w", err)
	}
	out := make([]PersistedContribution, 0, len(rows))
	for _, row := range rows {
		out = append(out, PersistedContribution{ProjectName: row.ProjectName, ResourceID: row.ResourceID, ResourceKind: row.ResourceKind, CapabilityID: row.CapabilityID, Description: row.Contribution})
	}
	return out, nil
}
