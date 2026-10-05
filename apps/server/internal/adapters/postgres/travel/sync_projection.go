package travelpg

import (
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/modules/travel/album"
	"tripfolio/server/internal/modules/travel/document"
	"tripfolio/server/internal/modules/travel/reservation"
)

func SyncPhoto(row dbgen.Photo) (album.Resource, error)                   { return toPhoto(row) }
func SyncDocument(row dbgen.Document) (document.Resource, error)          { return toDocument(row) }
func SyncReservation(row dbgen.Reservation) (reservation.Resource, error) { return toReservation(row) }
