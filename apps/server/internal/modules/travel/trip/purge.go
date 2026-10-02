package trip

import (
	"context"
	"errors"
	"time"
)

type PurgePlan struct {
	Completed bool
	Prefixes  []string
	Keys      []string
	NotBefore time.Time
}

type PurgeRun interface {
	Prepare(context.Context) (PurgePlan, error)
	SetObjects(context.Context, []string) ([]string, error)
	ObjectRemoved(context.Context, string) error
	Finish(context.Context) error
	Fail(context.Context, string) error
	Close()
}
type PurgeStore interface {
	Acquire(context.Context, PurgeJobArgs) (PurgeRun, error)
}
type PurgeObjects interface {
	ListKeys(context.Context, string) ([]string, error)
	PurgeKey(context.Context, string) error
}
type PurgeError struct {
	Code  string
	Cause error
}

func (e *PurgeError) Error() string { return e.Code }
func (e *PurgeError) Unwrap() error { return e.Cause }

type PurgeWaiting struct{ Until time.Time }

func (e *PurgeWaiting) Error() string { return "UPLOAD_AUTHORIZATION_ACTIVE" }

type PurgeBusy struct{}

func (*PurgeBusy) Error() string { return "PURGE_BUSY" }

type Purger struct {
	store   PurgeStore
	objects PurgeObjects
}

func NewPurger(store PurgeStore, objects PurgeObjects) *Purger {
	return &Purger{store: store, objects: objects}
}

// Purge only returns success after object verification and the final database transaction.
func (p *Purger) Purge(ctx context.Context, args PurgeJobArgs) error {
	run, err := p.store.Acquire(ctx, args)
	if err != nil {
		return err
	}
	defer run.Close()
	fail := func(code string, cause error) error {
		var classified *PurgeError
		if errors.As(cause, &classified) {
			code = classified.Code
		}
		// Cancellation must not leave a permanently running job without a retry entry point.
		failureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if saveErr := run.Fail(failureCtx, code); saveErr != nil {
			return &PurgeError{Code: "PURGE_STATE_UNAVAILABLE", Cause: saveErr}
		}
		return &PurgeError{Code: code, Cause: cause}
	}
	plan, err := run.Prepare(ctx)
	if err != nil {
		return fail("PURGE_PREPARE_FAILED", err)
	}
	if plan.Completed {
		return nil
	}
	if p.objects == nil {
		return fail("OBJECTSTORE_UNAVAILABLE", errors.New("object store is required for permanent cleanup"))
	}
	if time.Now().Before(plan.NotBefore) {
		return &PurgeWaiting{Until: plan.NotBefore}
	}
	keys := append([]string{}, plan.Keys...)
	for _, prefix := range plan.Prefixes {
		listed, err := p.objects.ListKeys(ctx, prefix)
		if err != nil {
			return fail("OBJECT_LIST_FAILED", err)
		}
		keys = append(keys, listed...)
	}
	keys, err = run.SetObjects(ctx, keys)
	if err != nil {
		return fail("PURGE_STATE_UNAVAILABLE", err)
	}
	for _, key := range keys {
		if err = p.objects.PurgeKey(ctx, key); err != nil {
			return fail("OBJECT_DELETE_FAILED", err)
		}
		if err = run.ObjectRemoved(ctx, key); err != nil {
			return fail("PURGE_STATE_UNAVAILABLE", err)
		}
	}
	// Re-list the exact asset prefixes: orphan attempts and delete markers also count.
	for _, prefix := range plan.Prefixes {
		left, err := p.objects.ListKeys(ctx, prefix)
		if err != nil {
			return fail("OBJECT_VERIFY_FAILED", err)
		}
		if len(left) > 0 {
			return fail("OBJECTS_REMAIN", errors.New("objects remain in target scope"))
		}
	}
	if err = run.Finish(ctx); err != nil {
		return fail("PURGE_ROWS_FAILED", err)
	}
	return nil
}
