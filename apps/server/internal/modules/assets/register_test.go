package assets_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/assets"
	"tripfolio/server/internal/modules/metadata"
)

func TestRegisterMetadataWithoutObjectAuthorizer(t *testing.T) {
	f := newFixture(t)
	limits := metadata.Current().UploadLimits
	svc := assets.NewService(assets.Deps{Keys: f.objects, Clock: f.clock, Limits: assets.UploadLimits{ImageMaxBytes: limits.ImageMaxBytes, PDFMaxBytes: limits.PDFMaxBytes, ImageMediaTypes: limits.ImageMediaTypes, PDFMediaTypes: limits.PDFMediaTypes}})
	in := assets.CreateInput{ID: uuid.New(), Scope: assets.ScopeTrip, TripID: &f.tripID, OriginalName: " pending.png ", ExpectedSize: 123, DeclaredMediaType: "IMAGE/PNG; charset=x"}
	register := func(input assets.CreateInput) (write.Result, error) {
		return f.uow.Run(context.Background(), write.Request{AccountID: f.actor.AccountID, OperationID: uuid.New()}, func(ctx context.Context, scope write.Scope, repo assets.Repo) error {
			_, err := svc.RegisterInTransaction(ctx, scope, repo, f.actor.AccountID, input)
			return err
		}, nil)
	}
	result, err := register(in)
	if err != nil {
		t.Fatal(err)
	}
	row, found, err := f.store.Get(context.Background(), f.actor.AccountID, in.ID)
	if err != nil || !found || row.Status != assets.StatusUploading || row.UploadAttempt != 1 || row.OriginalName != "pending.png" || row.Version != 1 {
		t.Fatalf("registration %+v %v", row, err)
	}
	if row.SHA256 != nil || row.ByteSize != nil || row.MediaType != nil || len(f.objects.uploadKeys) != 0 || len(f.objects.downloadKeys) != 0 {
		t.Fatal("metadata pretended verified or signed URL")
	}
	attempt, found, err := f.store.Attempt(context.Background(), f.actor.AccountID, in.ID)
	if err != nil || !found || attempt.ExpectedSize != 123 || attempt.DeclaredMediaType != "image/png" || result.Primary == nil {
		t.Fatalf("attempt %+v %v", attempt, err)
	}
	if _, err := register(in); err == nil {
		t.Fatal("stable ID reused")
	}
	_, err = f.uow.Run(context.Background(), write.Request{AccountID: f.actor.AccountID, OperationID: uuid.New()}, func(ctx context.Context, scope write.Scope, repo assets.Repo) error {
		_, err := svc.CreateInTransaction(ctx, scope, repo, f.actor.AccountID, in)
		return err
	}, nil)
	if e, ok := apperr.As(err); !ok || e.Status != 503 {
		t.Fatalf("legacy storage readiness changed: %v", err)
	}
}
