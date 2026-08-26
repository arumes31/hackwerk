package web

import (
	"errors"
	"net/http"
	"strconv"

	"example.invalid/hackplan/internal/maptile"
	"github.com/go-chi/chi/v5"
)

func registerMapTileRoutes(router chi.Router, client *maptile.Client) {
	router.Get("/map/tiles/{z}/{x}/{y}", func(response http.ResponseWriter, request *http.Request) {
		z, zErr := strconv.Atoi(chi.URLParam(request, "z"))
		x, xErr := strconv.Atoi(chi.URLParam(request, "x"))
		y, yErr := strconv.Atoi(chi.URLParam(request, "y"))
		if zErr != nil || xErr != nil || yErr != nil {
			http.Error(response, "Ungültige Kartenkachel.", http.StatusBadRequest)
			return
		}
		tile, err := client.Fetch(request.Context(), z, x, y)
		if err != nil {
			if errors.Is(err, maptile.ErrInvalidCoordinate) {
				http.Error(response, "Ungültige Kartenkachel.", http.StatusBadRequest)
				return
			}
			http.Error(response, "Kartenkachel vorübergehend nicht verfügbar.", http.StatusBadGateway)
			return
		}
		response.Header().Set("Content-Type", tile.ContentType)
		response.Header().Set("Cache-Control", "private, max-age=86400")
		if tile.ETag != "" {
			response.Header().Set("ETag", tile.ETag)
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(tile.Data)
	})
}
