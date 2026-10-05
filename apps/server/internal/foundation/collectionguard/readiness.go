package collectionguard

import "tripfolio/server/internal/foundation/apperr"

// Readiness contains verified server-side prerequisites, not account enablement state.
// Passing Check never persists or enables v2; activation must be separately authorized.
type Readiness struct {
	BaselineReady   bool
	PushComplete    bool
	RESTGuardsReady bool
	WebGuardsReady  bool
}

func (r Readiness) Check() error {
	if !r.BaselineReady || !r.PushComplete || !r.RESTGuardsReady || !r.WebGuardsReady {
		return apperr.Conflicted("SYNC_NOT_READY", "同步基线、完整推送及网页集合保护尚未全部就绪")
	}
	return nil
}
