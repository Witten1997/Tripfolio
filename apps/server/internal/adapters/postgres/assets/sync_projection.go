package assetspg

import (
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/modules/assets"
)

func SyncResource(row dbgen.Asset) (assets.Resource, error) { return toResource(row) }
