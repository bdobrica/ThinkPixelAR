package postgres

import (
	"context"
	"database/sql"
	"time"

	app "github.com/bdobrica/ThinkPixelAR/internal/app/session"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/workspace"
)

// WithResumeCandidate serializes bounded infrastructure mutations with close,
// failure, publication and other resume workers. A point-in-time fence before a
// provider call is insufficient: cleanup must not overtake an in-flight create.
// The callback must honor ctx, return only after its provider call has ended,
// and must not call back into the database. Ambiguous results retain the journal.
func (s *SessionResumes) WithResumeCandidate(ctx context.Context, i app.ResumeIntent, cleanup bool, fn func(context.Context) error) error {
	if fn == nil || i.Request.Validate() != nil {
		return workspace.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if !cleanup && s.access(ctx, i.Request) != nil {
		return workspace.ErrUnavailable
	}
	return s.transaction(ctx, i.Request.Caller.TenantID, i.Request.SessionID, func(tx *sql.Tx, v resumeSession) error {
		saved, result, err := resumeLoad(ctx, tx, i.Request)
		if err != nil {
			return err
		}
		if saved.Digest() != i.Digest() {
			return workspace.ErrIntegrity
		}
		if cleanup {
			if result.State != "DEGRADED" {
				return workspace.ErrConflict
			}
		} else {
			if result.State != "RESUMING" || !time.Now().Before(i.Deadline) {
				return workspace.ErrConflict
			}
			if err = s.fence(ctx, tx, i, v); err != nil {
				return err
			}
			if s.policy(ctx, cloneResume(i)) != nil {
				return workspace.ErrUnavailable
			}
		}
		return fn(ctx)
	})
}
