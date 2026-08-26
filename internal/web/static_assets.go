package web

import (
	"io/fs"
	"net/http"
)

const immutableAssetCacheControl = "public, max-age=31536000, immutable"

func staticAssetHandler(public fs.FS, versions map[string]string) http.Handler {
	files := http.StripPrefix("/assets/", http.FileServer(http.FS(public)))

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		digest, known := versions[request.URL.Path]
		if !known {
			files.ServeHTTP(response, request)
			return
		}

		etag := `"` + digest + `"`
		response.Header().Set("ETag", etag)
		if request.URL.Query().Get("v") == digest[:12] {
			response.Header().Set("Cache-Control", immutableAssetCacheControl)
		} else {
			response.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
		}
		if request.Header.Get("If-None-Match") == etag {
			response.WriteHeader(http.StatusNotModified)
			return
		}

		files.ServeHTTP(response, request)
	})
}
