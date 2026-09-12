package httpapi

import (
	"context"

	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// GetMetadata 实现 GET /metadata：把业务元数据转换为契约模型。
func (h *Handler) GetMetadata(_ context.Context, _ generated.GetMetadataRequestObject) (generated.GetMetadataResponseObject, error) {
	return generated.GetMetadata200JSONResponse(generated.MetadataResponse{Data: toAPIMetadata(h.metadata)}), nil
}

func toAPIMetadata(m metadata.Metadata) generated.Metadata {
	currencies := make([]generated.Currency, 0, len(m.Currencies))
	for _, c := range m.Currencies {
		currencies = append(currencies, generated.Currency{
			Code:       generated.CurrencyCode(c.Code),
			MinorUnits: int32(c.MinorUnits),
		})
	}
	return generated.Metadata{
		ProtocolVersion:       int32(m.ProtocolVersion),
		DefaultCurrencyCode:   generated.CurrencyCode(m.DefaultCurrencyCode),
		Currencies:            currencies,
		LedgerKinds:           m.LedgerKinds,
		ExpenseCategoryIcons:  m.ExpenseCategoryIcons,
		PackingCategories:     m.PackingCategories,
		PackingStatuses:       m.PackingStatuses,
		PackingLibraryVersion: m.PackingLibraryVersion,
		ItineraryKinds:        m.ItineraryKinds,
		ItineraryStatuses:     m.ItineraryStatuses,
		ReservationKinds:      m.ReservationKinds,
		UploadLimits: generated.UploadLimits{
			ImageMaxBytes:   m.UploadLimits.ImageMaxBytes,
			PdfMaxBytes:     m.UploadLimits.PDFMaxBytes,
			ImageMediaTypes: m.UploadLimits.ImageMediaTypes,
			PdfMediaTypes:   m.UploadLimits.PDFMediaTypes,
		},
		Map: generated.MapSettings{
			Provider:         generated.MapSettingsProvider(m.Map.Provider),
			CoordinateSystem: generated.MapSettingsCoordinateSystem(m.Map.CoordinateSystem),
		},
	}
}
