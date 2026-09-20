package reconciliation

import (
	"context"
	"testing"
	"time"

	work "github.com/bdobrica/ThinkPixelAR/internal/domain/reconciliation"
	"github.com/bdobrica/ThinkPixelAR/internal/ports/sandbox"
	"github.com/bdobrica/ThinkPixelAR/internal/primitives"
)

type workerFixture struct {
	claim    *work.Work
	observed sandbox.ComputeObservation
	done     bool
	code     string
	err      error
}

func (f *workerFixture) ClaimCompute(context.Context, primitives.ID, primitives.ID, time.Duration, int) ([]*work.Work, error) {
	return []*work.Work{f.claim}, nil
}
func (f *workerFixture) FinishCompute(_ context.Context, _ *work.Work, done bool, code string, _ time.Duration) error {
	f.done = done
	f.code = code
	return nil
}
func (f *workerFixture) Reconcile(ctx context.Context, _ primitives.ID, id primitives.ID) (sandbox.ComputeObservation, error) {
	if _, ok := ctx.Deadline(); !ok || id != f.claim.TargetID() {
		panic("unbounded or misdirected work")
	}
	return f.observed, f.err
}

func TestWorkerMonitorsReadinessAndFinishesOnlyConfirmedRelease(t *testing.T) {
	for _, state := range []sandbox.State{sandbox.Ready, sandbox.Released, sandbox.Unknown} {
		t.Run(string(state), func(t *testing.T) {
			id := primitives.ID("01900000-0000-7000-8000-000000000001")
			now := time.Now()
			claim, err := work.New(id, id, "sandbox.reconcile", "sandbox", id, now, now)
			if err != nil {
				t.Fatal(err)
			}
			if err = claim.Claim(id, now.Add(time.Minute), now); err != nil {
				t.Fatal(err)
			}
			f := &workerFixture{claim: claim, observed: sandbox.ComputeObservation{State: state, Code: "COMPUTE_READY", Converged: state != sandbox.Unknown}}
			if state == sandbox.Unknown {
				f.err = sandbox.ErrConflict
			}
			worker, err := NewWorker(f, f, time.Minute, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			n, err := worker.RunBatch(context.Background(), id, id, 1)
			if n != 1 || err != nil || f.done != (state == sandbox.Released) {
				t.Fatal("invalid completion", n, err, f.done)
			}
			if state == sandbox.Unknown && f.code != "FENCE_CHANGED" {
				t.Fatal("unsanitized error", f.code)
			}
		})
	}
}
