package httpapi

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/transport/httpapi/generated"
)

type ledgerTemplateResponse []byte

func (body ledgerTemplateResponse) VisitDownloadLedgerImportTemplateResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="tripfolio-ledger-template.xlsx"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(body)
	return err
}

func (h *Handler) DownloadLedgerImportTemplate(ctx context.Context, req generated.DownloadLedgerImportTemplateRequestObject) (generated.DownloadLedgerImportTemplateResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, notWired()
	}
	file, err := h.ledger.ImportTemplate(ctx, a, uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	return ledgerTemplateResponse(file), nil
}

func readLedgerWorkbook(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, apperr.BadRequest("INVALID_IMPORT_FILE", "请选择 .xlsx 文件")
	}
	data, err := io.ReadAll(io.LimitReader(body, finance.MaxLedgerImportBytes+1))
	if err != nil {
		return nil, apperr.BadRequest("INVALID_IMPORT_FILE", "文件上传未完成，请重试")
	}
	if len(data) == 0 || len(data) > finance.MaxLedgerImportBytes {
		return nil, apperr.BadRequest("INVALID_IMPORT_FILE", "请上传不超过 5 MB 的 .xlsx 文件")
	}
	return data, nil
}

func (h *Handler) PreviewLedgerImport(ctx context.Context, req generated.PreviewLedgerImportRequestObject) (generated.PreviewLedgerImportResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, notWired()
	}
	file, err := readLedgerWorkbook(req.Body)
	if err != nil {
		return nil, err
	}
	preview, err := h.ledger.PreviewImport(ctx, a, uuid.UUID(req.TripId), file)
	if err != nil {
		return nil, err
	}
	return generated.PreviewLedgerImport200JSONResponse{Data: preview}, nil
}

func (h *Handler) CommitLedgerImport(ctx context.Context, req generated.CommitLedgerImportRequestObject) (generated.CommitLedgerImportResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, notWired()
	}
	file, err := readLedgerWorkbook(req.Body)
	if err != nil {
		return nil, err
	}
	result, err := h.ledger.Import(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), file, req.Params.PreviewDigest)
	if err != nil {
		return nil, err
	}
	return generated.CommitLedgerImport201JSONResponse{Data: result}, nil
}
