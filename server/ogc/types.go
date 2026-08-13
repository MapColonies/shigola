package ogc

// The JSON shapes of the OGC API - Tiles resources this service serves.
//
// Field order follows the specification's own tables rather than Go convention,
// so that a document can be read alongside the spec it implements.

// Link is an OGC API link object (OGC API - Common, Part 1).
type Link struct {
	// Rel is the relation type: what this link is to the resource carrying it.
	// See the rel* constants.
	Rel string `json:"rel"`
	// Href is the absolute URL of the linked resource, or a URI template when
	// Templated is set.
	Href string `json:"href"`
	// Type is the media type of the linked resource.
	Type string `json:"type,omitempty"`
	// Title is a human-readable label.
	Title string `json:"title,omitempty"`
	// Templated marks Href as a URI template the client fills in.
	Templated bool `json:"templated,omitempty"`
}

// Link relation types.
//
// The unprefixed ones are IANA-registered; the others are OGC's own, which the
// specification requires be given as full URIs.
const (
	relSelf        = "self"
	relServiceDesc = "service-desc"
	relConformance = "conformance"
	relData        = "data"
	relItem        = "item"

	relTilingSchemes  = "http://www.opengis.net/def/rel/ogc/1.0/tiling-schemes"
	relTilingScheme   = "http://www.opengis.net/def/rel/ogc/1.0/tiling-scheme"
	relTilesetsVector = "http://www.opengis.net/def/rel/ogc/1.0/tilesets-vector"
)

// LandingPage is the service's root document.
type LandingPage struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Links       []Link `json:"links"`
}

// Conformance declares the conformance classes this service implements.
type Conformance struct {
	ConformsTo []string `json:"conformsTo"`
}

// conformsTo is the conformance declaration from ADR-0004.
//
// A class appears here only if the service satisfies it, since the declaration
// is what a client trusts when deciding how to talk to the service.
var conformsTo = []string{
	"http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/core",
	"http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/landingPage",
	"http://www.opengis.net/spec/ogcapi-common-2/1.0/conf/collections",
	"http://www.opengis.net/spec/ogcapi-common-1/1.0/conf/json",
	"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/core",
	"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/tileset",
	"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/tilesets-list",
	"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/mvt",
	"http://www.opengis.net/spec/ogcapi-tiles-1/1.0/conf/geodata-tilesets",
}

// TileMatrixSetItem is one entry in the /tileMatrixSets list. The full
// definition lives at the linked resource; this is the summary.
type TileMatrixSetItem struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	URI   string `json:"uri,omitempty"`
	CRS   string `json:"crs,omitempty"`
	Links []Link `json:"links"`
}

// TileMatrixSets is the /tileMatrixSets document.
type TileMatrixSets struct {
	TileMatrixSets []TileMatrixSetItem `json:"tileMatrixSets"`
}
