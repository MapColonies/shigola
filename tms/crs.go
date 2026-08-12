package tms

// CRS handling. morecantile delegates all of this to pyproj; this port has no
// PROJ dependency, so the coordinate reference systems it understands are
// exactly those described by the table below.
//
// Adding a CRS here does not make its grid usable: a grid also needs a
// Transformer (see transform.go) before it can convert to and from geographic
// coordinates. The table supplies the metadata that the OGC TMS model itself
// needs — metersPerUnit, axis order, and the geographic counterpart.

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Units of measure a CRS's horizontal axes can use, as named by OGC 17-083r4
// note (g) on metersPerUnit.
const (
	unitMetre  = "metre"
	unitDegree = "degree"
)

// wgs84SemiMajorMetre is the semi-major axis of the WGS 84 ellipsoid. GRS 80,
// used by the remaining bundled grids' datums, shares this value exactly, so a
// single constant covers every CRS in crsTable.
const wgs84SemiMajorMetre = 6378137.0

// crsInfo is the PROJ-free replacement for the pyproj.CRS metadata morecantile
// reads: the axis unit and ellipsoid feed metersPerUnit, axisInverted stands in
// for inspecting the first axis abbreviation, and geodetic names the geographic
// CRS that a projected CRS transforms to.
type crsInfo struct {
	// uri is the canonical OGC definition-server URI for the CRS.
	uri string
	// authority and code identify the CRS, e.g. ("EPSG", "3857").
	authority string
	code      string
	// unit is the unit of measure of the horizontal axes.
	unit string
	// semiMajorMetre is the semi-major axis of the CRS's ellipsoid, needed to
	// convert degrees to metres.
	semiMajorMetre float64
	// geographic reports whether the CRS is itself a geographic (lon/lat) CRS,
	// in which case converting to geographic coordinates is the identity.
	geographic bool
	// axisInverted reports whether the CRS's own axis order is (lat, lon)
	// rather than (lon, lat). It is only consulted when a TileMatrixSet
	// document does not declare orderedAxes.
	axisInverted bool
}

// crsTable is the CRS metadata this port knows, keyed by "AUTHORITY:CODE".
//
// It covers every CRS referenced by the 13 bundled grid definitions. Values are
// static properties of the CRSs themselves, taken from the EPSG registry.
var crsTable = map[string]crsInfo{
	// Geographic CRSs.
	"OGC:CRS84": {
		uri:            "http://www.opengis.net/def/crs/OGC/1.3/CRS84",
		authority:      "OGC",
		code:           "CRS84",
		unit:           unitDegree,
		semiMajorMetre: wgs84SemiMajorMetre,
		geographic:     true,
		axisInverted:   false, // CRS84 is explicitly lon, lat
	},
	"EPSG:4326": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/4326",
		authority:      "EPSG",
		code:           "4326",
		unit:           unitDegree,
		semiMajorMetre: wgs84SemiMajorMetre,
		geographic:     true,
		axisInverted:   true, // EPSG:4326 is lat, lon
	},

	// Projected CRSs. Their grids are registered but gated on a Transformer.
	"EPSG:3857": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/3857",
		authority:      "EPSG",
		code:           "3857",
		unit:           unitMetre,
		semiMajorMetre: wgs84SemiMajorMetre,
	},
	"EPSG:3395": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/3395",
		authority:      "EPSG",
		code:           "3395",
		unit:           unitMetre,
		semiMajorMetre: wgs84SemiMajorMetre,
	},
	"EPSG:3035": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/3035",
		authority:      "EPSG",
		code:           "3035",
		unit:           unitMetre,
		semiMajorMetre: wgs84SemiMajorMetre,
		axisInverted:   true, // ETRS89-LAEA is northing, easting
	},
	"EPSG:3978": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/3978",
		authority:      "EPSG",
		code:           "3978",
		unit:           unitMetre,
		semiMajorMetre: wgs84SemiMajorMetre,
	},
	"EPSG:32631": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/32631",
		authority:      "EPSG",
		code:           "32631",
		unit:           unitMetre,
		semiMajorMetre: wgs84SemiMajorMetre,
	},
	"EPSG:5041": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/5041",
		authority:      "EPSG",
		code:           "5041",
		unit:           unitMetre,
		semiMajorMetre: wgs84SemiMajorMetre,
	},
	"EPSG:5042": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/5042",
		authority:      "EPSG",
		code:           "5042",
		unit:           unitMetre,
		semiMajorMetre: wgs84SemiMajorMetre,
	},
	"EPSG:2193": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/2193",
		authority:      "EPSG",
		code:           "2193",
		unit:           unitMetre,
		semiMajorMetre: wgs84SemiMajorMetre,
		axisInverted:   true, // NZTM2000 is northing, easting
	},
	"EPSG:5482": {
		uri:            "http://www.opengis.net/def/crs/EPSG/0/5482",
		authority:      "EPSG",
		code:           "5482",
		unit:           unitMetre,
		semiMajorMetre: wgs84SemiMajorMetre,
		axisInverted:   true, // RSRGD2000 Antarctic LCC is northing, easting
	},
}

// CRS identifies a coordinate reference system in a TileMatrixSet document.
//
// OGC allows a CRS to be given as a plain string, as an object holding a URI,
// or as an object holding a PROJJSON WKT dictionary. All three round-trip
// through JSON unchanged so that a grid definition can be served back verbatim.
type CRS struct {
	// String is set when the document gave the CRS as a bare string. This is
	// the form all 13 bundled grids use.
	String string
	// URI is set when the document gave {"uri": ...}.
	URI string
	// WKT is set when the document gave {"wkt": ...}, and is preserved as-is.
	WKT map[string]any
}

// UnmarshalJSON accepts the three CRS encodings OGC permits. A CRS given as
// {"referenceSystem": ...} is rejected, as in morecantile.
func (c *CRS) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		c.String = s
		return nil
	}

	var obj struct {
		URI             string         `json:"uri"`
		WKT             map[string]any `json:"wkt"`
		ReferenceSystem map[string]any `json:"referenceSystem"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return fmt.Errorf("tms: cannot decode crs: %w", err)
	}

	switch {
	case obj.ReferenceSystem != nil:
		return UnsupportedCRSError{
			CRS:    "referenceSystem",
			Reason: "MD_ReferenceSystem defined CRS is not supported",
		}
	case obj.URI != "":
		c.URI = obj.URI
	case obj.WKT != nil:
		c.WKT = obj.WKT
	default:
		return UnsupportedCRSError{Reason: "crs is empty"}
	}

	return nil
}

// MarshalJSON re-emits the CRS in the encoding it was given in.
func (c CRS) MarshalJSON() ([]byte, error) {
	switch {
	case c.String != "":
		return json.Marshal(c.String)
	case c.URI != "":
		return json.Marshal(struct {
			URI string `json:"uri"`
		}{URI: c.URI})
	case c.WKT != nil:
		return json.Marshal(struct {
			WKT map[string]any `json:"wkt"`
		}{WKT: c.WKT})
	default:
		return []byte("null"), nil
	}
}

// Identifier returns the CRS as the authority-and-code key used by crsTable,
// e.g. "EPSG:3857". A WKT-encoded CRS has no such identifier, since resolving
// one would require a PROJ backend.
func (c CRS) Identifier() (string, error) {
	raw := c.String
	if raw == "" {
		raw = c.URI
	}

	if raw == "" {
		return "", UnsupportedCRSError{
			Reason: "CRS is given as WKT; resolving it needs a PROJ backend",
		}
	}

	id, ok := parseCRSIdentifier(raw)
	if !ok {
		return "", UnsupportedCRSError{CRS: raw, Reason: "unrecognized CRS syntax"}
	}

	return id, nil
}

// info resolves the CRS's metadata from crsTable.
func (c CRS) info() (crsInfo, error) {
	id, err := c.Identifier()
	if err != nil {
		return crsInfo{}, err
	}

	meta, ok := crsTable[id]
	if !ok {
		return crsInfo{}, UnsupportedCRSError{
			CRS:    id,
			Reason: "not in this build's CRS table",
		}
	}

	return meta, nil
}

// parseCRSIdentifier reduces the CRS spellings that appear in TileMatrixSet
// documents to an "AUTHORITY:CODE" key.
//
// It understands the OGC definition-server URI form
// ("http://www.opengis.net/def/crs/EPSG/0/3857"), the URN form
// ("urn:ogc:def:crs:EPSG::2193"), and the short form ("EPSG:3857").
func parseCRSIdentifier(raw string) (string, bool) {
	s := strings.TrimSpace(raw)

	switch {
	case strings.Contains(s, "/def/crs/"):
		// .../def/crs/{authority}/{version}/{code}
		parts := strings.Split(strings.Trim(s[strings.Index(s, "/def/crs/")+len("/def/crs/"):], "/"), "/")
		if len(parts) < 2 {
			return "", false
		}
		authority, code := parts[0], parts[len(parts)-1]
		if authority == "" || code == "" {
			return "", false
		}

		return normalizeCRSKey(authority, code), true

	case strings.HasPrefix(strings.ToLower(s), "urn:ogc:def:crs:"):
		// urn:ogc:def:crs:{authority}:{version}:{code}
		parts := strings.Split(s, ":")
		if len(parts) < 6 {
			return "", false
		}
		authority, code := parts[4], parts[len(parts)-1]
		if authority == "" || code == "" {
			return "", false
		}

		return normalizeCRSKey(authority, code), true

	case strings.Count(s, ":") == 1:
		// {authority}:{code}
		parts := strings.SplitN(s, ":", 2)
		if parts[0] == "" || parts[1] == "" {
			return "", false
		}

		return normalizeCRSKey(parts[0], parts[1]), true
	}

	return "", false
}

// normalizeCRSKey canonicalises an authority and code into a crsTable key.
// CRS84 is spelled by OGC as both "CRS84" and "OGC:CRS84"; EPSG codes are
// numeric. Authorities are upper-cased so "epsg:4326" resolves.
func normalizeCRSKey(authority, code string) string {
	return strings.ToUpper(authority) + ":" + strings.ToUpper(code)
}

// ToURI returns the canonical OGC definition-server URI for the CRS,
// equivalent to morecantile's CRS_to_uri.
func (c CRS) ToURI() (string, error) {
	meta, err := c.info()
	if err != nil {
		return "", err
	}

	return meta.uri, nil
}

// EPSG returns the CRS's EPSG code, or 0 when the CRS is not an EPSG one (for
// instance OGC:CRS84).
func (c CRS) EPSG() (uint64, error) {
	meta, err := c.info()
	if err != nil {
		return 0, err
	}

	if meta.authority != "EPSG" {
		return 0, nil
	}

	code, err := strconv.ParseUint(meta.code, 10, 64)
	if err != nil {
		return 0, UnsupportedCRSError{CRS: meta.uri, Reason: "EPSG code is not numeric"}
	}

	return code, nil
}

// metersPerUnit returns the coefficient converting the CRS's units into metres.
//
// Ported from morecantile.utils.meters_per_unit. From note (g) in
// http://docs.opengeospatial.org/is/17-083r2/17-083r2.html#table_2: if the CRS
// uses metres for the horizontal dimensions then metersPerUnit is 1; if it uses
// degrees then metersPerUnit is 2*pi*a/360, where a is the ellipsoid's
// semi-major axis.
func (c crsInfo) metersPerUnit() (float64, error) {
	switch c.unit {
	case unitMetre:
		return 1.0, nil
	case unitDegree:
		return 2 * math.Pi * c.semiMajorMetre / 360.0, nil
	default:
		return 0, UnsupportedCRSError{
			CRS:    c.uri,
			Reason: fmt.Sprintf("unit %q has no metersPerUnit conversion", c.unit),
		}
	}
}

// orderedAxisInverted reports whether declared orderedAxes put the vertical
// axis first, i.e. (lat, lon) rather than (lon, lat).
//
// Ported from morecantile.models.ordered_axis_inverted.
func orderedAxisInverted(axes []string) bool {
	if len(axes) == 0 {
		return false
	}

	switch strings.ToUpper(axes[0]) {
	case "Y", "LAT", "N":
		return true
	default:
		return false
	}
}
