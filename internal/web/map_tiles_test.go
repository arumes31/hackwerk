package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.invalid/hackplan/internal/maptile"
	"github.com/go-chi/chi/v5"
)

func TestMapTileHandlerRejectsCoordinatesBeforeUpstreamRequest(t *testing.T) {
	client, err := maptile.New(maptile.Config{URLTemplate: "https://tiles.example/{z}/{x}/{y}.png", Timeout: time.Second, MaxResponseBytes: 64 << 10, MaxZoom: 19})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	registerMapTileRoutes(router, client)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/map/tiles/2/4/0", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
