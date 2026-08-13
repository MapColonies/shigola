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

// Extent is a collection's spatial extent, in CRS84.
type Extent struct {
	Spatial *SpatialExtent `json:"spatial,omitempty"`
}

// SpatialExtent is the bounding box half of an extent.
type SpatialExtent struct {
	// BBox is a list of bounding boxes; the first is the overall one.
	BBox [][]float64 `json:"bbox"`
	CRS  string      `json:"crs,omitempty"`
}

// CollectionDesc is the /collections/{collectionId} document.
type CollectionDesc struct {
	ID          string  `json:"id"`
	Title       string  `json:"title,omitempty"`
	Description string  `json:"description,omitempty"`
	Extent      *Extent `json:"extent,omitempty"`
	// DataType is "vector" for every collection this service publishes: tegola
	// produces MVT and nothing else (ADR-0001).
	DataType string   `json:"dataType,omitempty"`
	CRS      []string `json:"crs,omitempty"`
	Links    []Link   `json:"links"`
}

// Collections is the /collections document.
type Collections struct {
	Links       []Link           `json:"links"`
	Collections []CollectionDesc `json:"collections"`
}

// TileSetItem is one entry in a collection's tilesets list.
type TileSetItem struct {
	Title            string `json:"title,omitempty"`
	DataType         string `json:"dataType"`
	CRS              string `json:"crs"`
	TileMatrixSetID  string `json:"tileMatrixSetId"`
	TileMatrixSetURI string `json:"tileMatrixSetURI,omitempty"`
	Links            []Link `json:"links"`
}

// TileSets is the /collections/{collectionId}/tiles document.
type TileSets struct {
	Tilesets []TileSetItem `json:"tilesets"`
	Links    []Link        `json:"links"`
}

// TileMatrixLimits bounds the tiles that actually hold data at one zoom, so a
// client need not request tiles outside the collection's extent.
type TileMatrixLimits struct {
	TileMatrix string `json:"tileMatrix"`
	MinTileRow int64  `json:"minTileRow"`
	MaxTileRow int64  `json:"maxTileRow"`
	MinTileCol int64  `json:"minTileCol"`
	MaxTileCol int64  `json:"maxTileCol"`
}

// GeoDataLayer describes one vector layer inside a tileset's tiles.
type GeoDataLayer struct {
	ID            string `json:"id"`
	Title         string `json:"title,omitempty"`
	DataType      string `json:"dataType"`
	GeometryType  string `json:"geometryDimension,omitempty"`
	MinTileMatrix string `json:"minTileMatrix,omitempty"`
	MaxTileMatrix string `json:"maxTileMatrix,omitempty"`
}

// TileSetMetadata is the /collections/{collectionId}/tiles/{tileMatrixSetId}
// document: everything a client needs to request tiles of one collection in one
// tiling scheme.
type TileSetMetadata struct {
	Title            string             `json:"title,omitempty"`
	Description      string             `json:"description,omitempty"`
	DataType         string             `json:"dataType"`
	CRS              string             `json:"crs"`
	TileMatrixSetID  string             `json:"tileMatrixSetId"`
	TileMatrixSetURI string             `json:"tileMatrixSetURI,omitempty"`
	TileMatrixLimits []TileMatrixLimits `json:"tileMatrixSetLimits,omitempty"`
	BoundingBox      *BoundingBox       `json:"boundingBox,omitempty"`
	Layers           []GeoDataLayer     `json:"layers,omitempty"`
	Attribution      string             `json:"attribution,omitempty"`
	Links            []Link             `json:"links"`
}

// BoundingBox is a tileset's extent in a named CRS.
type BoundingBox struct {
	LowerLeft  []float64 `json:"lowerLeft"`
	UpperRight []float64 `json:"upperRight"`
	CRS        string    `json:"crs,omitempty"`
}

// Data types. tegola serves vector tiles only (ADR-0001).
const (
	dataTypeVector = "vector"
)

// CRS84 is the CRS a collection's extent is given in.
const crs84URI = "http://www.opengis.net/def/crs/OGC/1.3/CRS84"
