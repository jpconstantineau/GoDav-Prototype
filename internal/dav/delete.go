package dav

import (
	"io"
	"net/http"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

func deleteResource(w http.ResponseWriter, r *http.Request, service *files.Service, path string) {
	depths := r.Header.Values("Depth")
	request := files.DeleteRequest{}
	if len(depths) > 1 {
		http.Error(w, "Bad Request", 400)
		return
	}
	if len(depths) == 1 {
		depth := strings.TrimSpace(depths[0])
		switch {
		case strings.EqualFold(depth, "infinity"):
		case depth == "0" || depth == "1":
			request.FiniteDepth = true
		default:
			http.Error(w, "Bad Request", 400)
			return
		}
	}
	// Confirm an empty body before taking the publication gate, including unknown
	// length/chunked bodies. A receiving client never owns a SQLite transaction.
	var probe [1]byte
	n, err := io.ReadFull(r.Body, probe[:])
	if n != 0 {
		http.Error(w, "Unsupported Media Type", 415)
		return
	}
	if err != io.EOF {
		http.Error(w, "Bad Request", 400)
		return
	}
	if err := service.Delete(r.Context(), path, request, publicationExpectation(r)); err != nil {
		namespaceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
