// SPDX-License-Identifier: FSL-1.1-Apache-2.0

package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/sluicio/sluicio-app/pkg/httpserver"
)

// thirdPartyFile is where the image carries the generated list of
// third-party components (scripts/thirdparty, run in the Dockerfile).
// SLUICIO_THIRD_PARTY_FILE overrides it for a build laid out elsewhere.
const thirdPartyFile = "/usr/share/sluicio/third-party.json"

// getThirdParty: GET /api/v1/third-party - the third-party components
// built into the server components, with their licence and notice texts
// (issue #41).
//
// Public, like the licence page that reads it: it describes the software,
// not anyone's data, and whoever received the software is owed it whether
// or not they can sign in. Served from the file generated when the image
// was built, so it is exactly this version's list - the reason it is not
// a page in the documentation.
func (h *Handlers) getThirdParty(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(os.Getenv("SLUICIO_THIRD_PARTY_FILE"))
	if path == "" {
		path = thirdPartyFile
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		// A binary run outside its image (development) has no list; the
		// release images always do.
		httpserver.WriteError(w, http.StatusNotFound, "the third-party list is generated when the image is built and is not part of this build")
		return
	}
	if err != nil {
		h.Logger.Error("third-party list unreadable", "err", err, "path", path)
		httpserver.WriteError(w, http.StatusInternalServerError, "third-party list unreadable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(b)
}
