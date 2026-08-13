package ogc

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Media types this surface serves.
const (
	MediaTypeJSON     = "application/json"
	MediaTypeTileJSON = "application/json"
	MediaTypeMVT      = "application/vnd.mapbox-vector-tile"
	MediaTypeOpenAPI  = "application/vnd.oai.openapi+json;version=3.0"
)

// Format is a representation a resource can be served in (ADR-0005).
type Format string

const (
	// FormatJSON is the OGC JSON representation, and the default everywhere.
	FormatJSON Format = "json"
	// FormatTileJSON is the TileJSON 3.0 convenience representation of tileset
	// metadata.
	FormatTileJSON Format = "tilejson"
	// FormatMVT is a Mapbox Vector Tile.
	FormatMVT Format = "mvt"
)

// formatAliases are spellings of a format that this service accepts but never
// emits.
//
// "pbf" is what tegola's native routes call a Mapbox Vector Tile — the tile
// extension, and the `format` member of both the TileJSON this service serves
// and the capabilities document the native surface serves. Without this, a
// client that read either of those and asked for ?f=pbf would be refused for
// naming the format the way we named it to them.
//
// An alias is resolved before the resource's own formats are consulted, so it
// succeeds only where the format it names is actually served: ?f=pbf on a
// JSON-only resource is still a 400. It never becomes a Format value, so it
// cannot reach a link, a media type, or the list in an error.
var formatAliases = map[string]Format{
	"pbf": FormatMVT,
}

// mediaType returns the media type a format is served as.
func (f Format) mediaType() string {
	switch f {
	case FormatMVT:
		return MediaTypeMVT
	case FormatTileJSON:
		return MediaTypeTileJSON
	default:
		return MediaTypeJSON
	}
}

// ErrUnsupportedFormat reports a format a resource cannot be served in.
type ErrUnsupportedFormat struct {
	Requested string
	Supported []Format
}

func (e ErrUnsupportedFormat) Error() string {
	names := make([]string, 0, len(e.Supported))
	for _, f := range e.Supported {
		names = append(names, string(f))
	}

	return fmt.Sprintf("format %q is not supported for this resource; supported formats are %s",
		e.Requested, strings.Join(names, ", "))
}

// negotiate picks the representation to serve, from the ?f= query parameter and
// then the Accept header (ADR-0005).
//
// f wins outright when present: it is an explicit instruction, typically from a
// browser address bar whose Accept header asks for HTML the service does not
// serve. An unrecognised f is an error rather than a fallback, so a typo does
// not quietly return the wrong representation.
//
// supported must list the resource's formats, most preferred first; that first
// entry is what an absent or unselective Accept gets.
func negotiate(r *http.Request, supported ...Format) (Format, error) {
	if len(supported) == 0 {
		return "", ErrUnsupportedFormat{Requested: "", Supported: nil}
	}

	if requested := r.URL.Query().Get("f"); requested != "" {
		wanted := Format(requested)
		if alias, ok := formatAliases[strings.ToLower(requested)]; ok {
			wanted = alias
		}

		// The match returns the format from supported, never the request's
		// spelling of it, so a caller always receives the canonical value.
		for _, f := range supported {
			if strings.EqualFold(string(wanted), string(f)) {
				return f, nil
			}
		}

		// Reported as asked for, not as resolved: a client that sent ?f=pbf is
		// told about "pbf".
		return "", ErrUnsupportedFormat{Requested: requested, Supported: supported}
	}

	accept := strings.TrimSpace(r.Header.Get("Accept"))
	if accept == "" {
		return supported[0], nil
	}

	if best, ok := bestAccepted(accept, supported); ok {
		return best, nil
	}

	// An Accept header naming only types this resource cannot produce is a
	// client that would rather have something than nothing — every real map
	// client sends */* somewhere in the list — so the default is served rather
	// than a 406.
	return supported[0], nil
}

// acceptEntry is one media range from an Accept header.
type acceptEntry struct {
	mediaRange string
	quality    float64
	// order preserves the header's own ordering, which breaks quality ties.
	order int
}

// bestAccepted returns the supported format the Accept header prefers most.
func bestAccepted(accept string, supported []Format) (Format, bool) {
	entries := parseAccept(accept)

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].quality != entries[j].quality {
			return entries[i].quality > entries[j].quality
		}

		return entries[i].order < entries[j].order
	})

	for _, entry := range entries {
		if entry.quality == 0 {
			// q=0 means "not acceptable"
			continue
		}

		for _, f := range supported {
			if mediaRangeMatches(entry.mediaRange, f.mediaType()) {
				return f, true
			}
		}
	}

	return "", false
}

// parseAccept splits an Accept header into its media ranges and q values.
func parseAccept(accept string) []acceptEntry {
	var entries []acceptEntry

	for i, part := range strings.Split(accept, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		mediaRange, params, err := mime.ParseMediaType(part)
		if err != nil {
			// A malformed range is skipped rather than failing the request: the
			// rest of the header may still express something usable.
			continue
		}

		quality := 1.0
		if q, ok := params["q"]; ok {
			if parsed, err := strconv.ParseFloat(q, 64); err == nil {
				quality = parsed
			}
		}

		entries = append(entries, acceptEntry{mediaRange: mediaRange, quality: quality, order: i})
	}

	return entries
}

// mediaRangeMatches reports whether an Accept media range covers a media type.
func mediaRangeMatches(mediaRange, mediaType string) bool {
	if mediaRange == "*/*" {
		return true
	}

	// the served media type may carry parameters the range does not
	if idx := strings.IndexByte(mediaType, ';'); idx >= 0 {
		mediaType = strings.TrimSpace(mediaType[:idx])
	}

	if strings.EqualFold(mediaRange, mediaType) {
		return true
	}

	if strings.HasSuffix(mediaRange, "/*") {
		return strings.EqualFold(
			strings.TrimSuffix(mediaRange, "/*"),
			strings.SplitN(mediaType, "/", 2)[0],
		)
	}

	return false
}

// writeJSON serves v as JSON in the given format's media type.
func writeJSON(w http.ResponseWriter, format Format, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("encoding response: %w", err))
		return
	}

	w.Header().Set("Content-Type", format.mediaType())
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(body); err != nil {
		// The status line is already sent, so this can only be reported.
		logf("ogc: writing response: %v", err)
	}
}

// writeError serves an error as an OGC exception document.
func writeError(w http.ResponseWriter, status int, err error) {
	body, jsonErr := json.Marshal(struct {
		Code        string `json:"code"`
		Description string `json:"description"`
	}{
		Code:        http.StatusText(status),
		Description: err.Error(),
	})
	if jsonErr != nil {
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", MediaTypeJSON)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	w.Write(body)
}
