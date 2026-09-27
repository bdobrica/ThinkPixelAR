package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/bdobrica/ThinkPixelAR/internal/domain/runtimeevent"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/persistence"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

func (r *eventRepository) ReadNext(ctx context.Context, sessionID primitives.ID, after uint64, now time.Time) (persistence.EventRead, error) {
	var result persistence.EventRead
	// A single statement keeps bounds and the event in one MVCC snapshot without
	// holding a stream lock or transaction while the network consumer is slow.
	rows, err := r.tx.QueryContext(ctx, `WITH bounds AS (
 SELECT COALESCE((SELECT last_sequence FROM runtime_event_streams WHERE tenant_id=$1 AND session_id=$2),0) AS latest,
 (SELECT min(sequence) FROM runtime_events WHERE tenant_id=$1 AND session_id=$2 AND (retain_until IS NULL OR retain_until>$4)) AS earliest
 ) SELECT COALESCE(b.earliest,b.latest+1),b.latest,e.event_id,e.execution_id,e.attempt_id,e.sequence,e.aggregate_version,e.event_type,e.occurred_at,e.recorded_at,e.source,e.classification,e.payload,e.request_id,e.trace_id,e.span_id,e.retention_policy,e.retain_until
 FROM bounds b LEFT JOIN LATERAL (
 SELECT * FROM runtime_events WHERE tenant_id=$1 AND session_id=$2 AND sequence>$3 AND (retain_until IS NULL OR retain_until>$4) ORDER BY sequence LIMIT 1
 ) e ON true`, r.tenantID, sessionID, after, now)
	if err != nil {
		return result, wrap("read event", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return result, rows.Err()
	}
	var id, execution, attempt, typ, source, classification, request, trace, span, policy sql.NullString
	var sequence, version sql.NullInt64
	var occurred, recorded, expiry sql.NullTime
	var payload []byte
	if err = rows.Scan(&result.Earliest, &result.Latest, &id, &execution, &attempt, &sequence, &version, &typ, &occurred, &recorded, &source, &classification, &payload, &request, &trace, &span, &policy, &expiry); err != nil {
		return result, wrap("read event", err)
	}
	if id.Valid {
		var until *time.Time
		if expiry.Valid {
			until = &expiry.Time
		}
		result.Event, err = runtimeevent.New(primitives.ID(id.String), r.tenantID, sessionID, primitives.ID(execution.String), primitives.ID(attempt.String), uint64(sequence.Int64), uint64(version.Int64), runtimeevent.Type(typ.String), occurred.Time, recorded.Time, runtimeevent.Source(source.String), runtimeevent.Classification(classification.String), payload, runtimeevent.Correlation{RequestID: primitives.ID(request.String), TraceID: trace.String, SpanID: span.String}, policy.String, until)
		if err != nil {
			return persistence.EventRead{}, wrap("restore event", err)
		}
	}
	return result, rows.Err()
}
