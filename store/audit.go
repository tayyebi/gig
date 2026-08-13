package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AuditLog is an immutable record of a privileged or sensitive action.
type AuditLog struct {
	ID          int64
	ActorUserID *int64
	ActorIP     string
	Action      string
	EntityType  string
	EntityID    string
	Details     []byte
	CreatedAt   time.Time
}

// LogAction records an audited action. actorUserID may be nil for anonymous
// system actions; details is a JSON object.
func (s *Store) LogAction(ctx context.Context, actorUserID *int64, actorIP, action, entityType, entityID string, details map[string]any) error {
	var uid sql.NullInt64
	if actorUserID != nil {
		uid = sql.NullInt64{Int64: *actorUserID, Valid: true}
	}
	var raw json.RawMessage
	if details == nil {
		raw = json.RawMessage("{}")
	} else {
		b, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshal audit details: %w", err)
		}
		raw = b
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (actor_user_id, actor_ip, action, entity_type, entity_id, details)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		uid, actorIP, action, nullIfEmpty(entityType), nullIfEmpty(entityID), raw,
	)
	if err != nil {
		return fmt.Errorf("log action %s: %w", action, err)
	}
	return nil
}

// RecentActions returns the latest audit log entries.
func (s *Store) RecentActions(ctx context.Context, limit int) ([]AuditLog, error) {
	if limit < 1 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, actor_user_id, actor_ip, action, entity_type, entity_id, details, created_at
		FROM audit_log
		ORDER BY id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent audit actions: %w", err)
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var a AuditLog
		var uid sql.NullInt64
		var entityType, entityID sql.NullString
		if err := rows.Scan(&a.ID, &uid, &a.ActorIP, &a.Action, &entityType, &entityID, &a.Details, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		if uid.Valid {
			id := uid.Int64
			a.ActorUserID = &id
		}
		if entityType.Valid {
			a.EntityType = entityType.String
		}
		if entityID.Valid {
			a.EntityID = entityID.String
		}
		logs = append(logs, a)
	}
	return logs, rows.Err()
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}
