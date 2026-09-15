package httpapi

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/share"
	"tripfolio/server/internal/transport/httpapi/generated"
)

func TestShareHandlersRejectWrongIdentity(t *testing.T) {
	h := &Handler{}
	owner := actor.WithActor(context.Background(), actor.Actor{AccountID: uuid.New()})
	viewer := share.WithViewer(context.Background(), share.Viewer{AccountID: uuid.New(), TripID: uuid.New(), ShareID: uuid.New()})
	for name, call := range map[string]func(context.Context) error{
		"get": func(ctx context.Context) error {
			_, e := h.GetTripShare(ctx, generated.GetTripShareRequestObject{})
			return e
		},
		"enable": func(ctx context.Context) error {
			_, e := h.EnableTripShare(ctx, generated.EnableTripShareRequestObject{})
			return e
		},
		"rotate": func(ctx context.Context) error {
			_, e := h.RotateTripShare(ctx, generated.RotateTripShareRequestObject{})
			return e
		},
		"disable": func(ctx context.Context) error {
			_, e := h.DisableTripShare(ctx, generated.DisableTripShareRequestObject{})
			return e
		},
	} {
		t.Run(name, func(t *testing.T) { assertShareAuth(t, call(viewer)) })
	}
	for name, call := range map[string]func(context.Context) error{
		"trip": func(ctx context.Context) error {
			_, e := h.GetSharedTrip(ctx, generated.GetSharedTripRequestObject{})
			return e
		},
		"items": func(ctx context.Context) error {
			_, e := h.ListSharedItineraryItems(ctx, generated.ListSharedItineraryItemsRequestObject{})
			return e
		},
		"routes": func(ctx context.Context) error {
			_, e := h.GetSharedRoutes(ctx, generated.GetSharedRoutesRequestObject{})
			return e
		},
	} {
		t.Run(name, func(t *testing.T) { assertShareAuth(t, call(owner)) })
	}
}

func assertShareAuth(t *testing.T, err error) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Status != 401 || e.Code != "AUTH_REQUIRED" {
		t.Fatalf("want 401 AUTH_REQUIRED, got %v", err)
	}
}
