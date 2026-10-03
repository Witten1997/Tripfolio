package httpapi

import (
	"github.com/oapi-codegen/nullable"
	"tripfolio/server/internal/foundation/types"
)

func contentNullableLocal(n nullable.Nullable[string]) (bool, *types.LocalDateTime) {
	set, value := nullableString(n)
	if value == nil {
		return set, nil
	}
	v := types.LocalDateTime(*value)
	return set, &v
}
