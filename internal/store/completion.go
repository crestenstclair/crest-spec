package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/completion"
	"github.com/crestenstclair/crest-spec/internal/db"
)

// TransitionSource ties a derived state change to its originating event.
type TransitionSource struct {
	Type      string
	ID        string
	SessionID string
}

type StatusTransition struct {
	ID         string
	SubjectID  string
	FromStatus string
	ToStatus   string
	Reason     string
	Source     TransitionSource
	CreatedAt  time.Time
}

type CompletionBlocker struct {
	ID          string    `json:"id"`
	ProjectName string    `json:"project_name"`
	GoalID      string    `json:"goal_id,omitempty"`
	Category    string    `json:"category"`
	Reason      string    `json:"reason"`
	SourceType  string    `json:"source_type,omitempty"`
	SourceID    string    `json:"source_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ApplyCompletionProjection atomically persists current goal/project state and
// append-only transitions. Reapplying an identical projection is idempotent.
func (s *Store) ApplyCompletionProjection(ctx context.Context, projectName string, goals []completion.GoalProjection, project completion.ProjectProjection, source TransitionSource) (err error) {
	if project.ProjectName != projectName {
		return fmt.Errorf("apply completion projection: project mismatch %q != %q", project.ProjectName, projectName)
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("apply completion projection: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	q := s.queries.WithTx(tx)
	timestamp := now()
	var sessionID *string
	if source.SessionID != "" {
		value := source.SessionID
		sessionID = &value
	}

	for _, projection := range goals {
		current, queryErr := q.GetProjectGoal(ctx, projection.GoalID)
		if queryErr != nil {
			return fmt.Errorf("apply completion projection: get goal %s: %w", projection.GoalID, mapNotFound(queryErr))
		}
		if current.ProjectName != projectName || current.Active != 1 {
			return fmt.Errorf("apply completion projection: goal %s is not active in project %s", projection.GoalID, projectName)
		}
		if current.Status == string(projection.Status) {
			if current.StatusReason != projection.Reason {
				err = q.UpdateProjectGoalStatus(ctx, db.UpdateProjectGoalStatusParams{Status: current.Status, StatusReason: projection.Reason, UpdatedAt: timestamp, ID: projection.GoalID})
				if err != nil {
					return fmt.Errorf("apply completion projection: refresh goal %s explanation: %w", projection.GoalID, err)
				}
			}
			continue
		}
		err = q.UpdateProjectGoalStatus(ctx, db.UpdateProjectGoalStatusParams{Status: string(projection.Status), StatusReason: projection.Reason, UpdatedAt: timestamp, ID: projection.GoalID})
		if err != nil {
			return fmt.Errorf("apply completion projection: update goal %s: %w", projection.GoalID, err)
		}
		err = q.InsertGoalStatusHistory(ctx, db.InsertGoalStatusHistoryParams{
			ID: uuid.NewString(), GoalID: projection.GoalID, FromStatus: current.Status, ToStatus: string(projection.Status),
			Reason: projection.Reason, SourceType: source.Type, SourceID: source.ID, SessionID: sessionID, CreatedAt: timestamp,
		})
		if err != nil {
			return fmt.Errorf("apply completion projection: record goal %s transition: %w", projection.GoalID, err)
		}
	}

	currentProject, queryErr := q.GetProjectState(ctx, projectName)
	if queryErr != nil {
		return fmt.Errorf("apply completion projection: get project: %w", mapNotFound(queryErr))
	}
	if currentProject.CompletionStatus == string(project.Status) {
		if currentProject.CompletionReason != project.Reason {
			err = q.UpdateProjectCompletionStatus(ctx, db.UpdateProjectCompletionStatusParams{CompletionStatus: currentProject.CompletionStatus, CompletionReason: project.Reason, UpdatedAt: timestamp, ProjectName: projectName})
			if err != nil {
				return fmt.Errorf("apply completion projection: refresh project explanation: %w", err)
			}
		}
	} else {
		err = q.UpdateProjectCompletionStatus(ctx, db.UpdateProjectCompletionStatusParams{CompletionStatus: string(project.Status), CompletionReason: project.Reason, UpdatedAt: timestamp, ProjectName: projectName})
		if err != nil {
			return fmt.Errorf("apply completion projection: update project: %w", err)
		}
		err = q.InsertProjectStatusHistory(ctx, db.InsertProjectStatusHistoryParams{
			ID: uuid.NewString(), ProjectName: projectName, FromStatus: currentProject.CompletionStatus, ToStatus: string(project.Status),
			Reason: project.Reason, SourceType: source.Type, SourceID: source.ID, SessionID: sessionID, CreatedAt: timestamp,
		})
		if err != nil {
			return fmt.Errorf("apply completion projection: record project transition: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("apply completion projection: commit transaction: %w", err)
	}
	return nil
}

func (s *Store) ListGoalStatusHistory(ctx context.Context, goalID string) ([]StatusTransition, error) {
	rows, err := s.queries.ListGoalStatusHistory(ctx, goalID)
	if err != nil {
		return nil, fmt.Errorf("list goal status history: %w", err)
	}
	out := make([]StatusTransition, 0, len(rows))
	for _, row := range rows {
		out = append(out, statusTransition(row.ID, row.GoalID, row.FromStatus, row.ToStatus, row.Reason, row.SourceType, row.SourceID, row.SessionID, row.CreatedAt))
	}
	return out, nil
}

func (s *Store) ListProjectStatusHistory(ctx context.Context, projectName string) ([]StatusTransition, error) {
	rows, err := s.queries.ListProjectStatusHistory(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list project status history: %w", err)
	}
	out := make([]StatusTransition, 0, len(rows))
	for _, row := range rows {
		out = append(out, statusTransition(row.ID, row.ProjectName, row.FromStatus, row.ToStatus, row.Reason, row.SourceType, row.SourceID, row.SessionID, row.CreatedAt))
	}
	return out, nil
}

func (s *Store) ListCompletionBlockers(ctx context.Context, projectName string) ([]CompletionBlocker, error) {
	rows, err := s.queries.ListCompletionBlockers(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("list completion blockers: %w", err)
	}
	out := make([]CompletionBlocker, 0, len(rows))
	for _, row := range rows {
		goalID := ""
		if row.GoalID != nil {
			goalID = *row.GoalID
		}
		out = append(out, CompletionBlocker{ID: row.ID, ProjectName: row.ProjectName, GoalID: goalID, Category: row.Category, Reason: row.Reason, SourceType: row.SourceType, SourceID: row.SourceID, CreatedAt: parseTime(row.CreatedAt)})
	}
	return out, nil
}

func statusTransition(id, subjectID, from, to, reason, sourceType, sourceID string, sessionID *string, createdAt string) StatusTransition {
	source := TransitionSource{Type: sourceType, ID: sourceID}
	if sessionID != nil {
		source.SessionID = *sessionID
	}
	return StatusTransition{ID: id, SubjectID: subjectID, FromStatus: from, ToStatus: to, Reason: reason, Source: source, CreatedAt: parseTime(createdAt)}
}
