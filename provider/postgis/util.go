package postgis

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/basic"
	"github.com/MapColonies/shigola/config"
	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/provider"
	"github.com/go-spatial/geom"
	"github.com/jackc/pgx/v5/tracelog"
)

// genSQL will fill in the SQL field of a layer given a pool, and list of fields.
func genSQL(
	l *Layer,
	pool *connectionPoolCollector,
	tblname string,
	flds []string,
	buffer bool,
) (sql string, err error) {
	// we need to hit the database to see what the fields are.
	if len(flds) == 0 {
		sql := fmt.Sprintf(fldsSQL, tblname)

		//	if a subquery is set in the 'sql' config the subquery is set to the layer's
		//	'tablename' param. because of this case normal SQL token replacement needs to be
		//	applied to tablename SQL generation
		tile := provider.NewTile(0, 0, 0, 64, shigola.WebMercator)
		sql, err = replaceTokens(sql, l, tile, buffer)
		if err != nil {
			return "", err
		}

		rows, err := pool.Query(context.Background(), sql)
		if err != nil {
			return "", err
		}
		defer rows.Close()

		fdescs := rows.FieldDescriptions()
		if len(fdescs) == 0 {
			return "", fmt.Errorf("no fields were returned for table %v", tblname)
		}

		// to avoid field names possibly colliding with Postgres keywords,
		// we wrap the field names in quotes
		for i := range fdescs {
			flds = append(flds, string(fdescs[i].Name))
		}
	}

	fgeom := -1

	for i, f := range flds {
		if f == l.geomField {
			fgeom = i
		}
		flds[i] = fmt.Sprintf(`"%v"`, flds[i])
	}

	// to avoid field names possibly colliding with Postgres keywords,
	// we wrap the field names in quotes

	// The geometry is selected as-is: ST_AsMVT takes it from here, and the
	// bytes never reach this process. The removed standard type wrapped it in
	// ST_AsBinary and decoded the WKB in Go.
	if fgeom == -1 {
		flds = append(flds, fmt.Sprintf(`"%v" AS "%[1]v"`, l.geomField))
	} else {
		flds[fgeom] = fmt.Sprintf(`"%v" AS "%[1]v"`, l.geomField)
	}

	// add required id field
	if l.idField != "" {
		flds = append(flds, fmt.Sprintf(`"%v"`, l.idField))
	}

	selectClause := strings.Join(flds, ", ")

	return fmt.Sprintf(mvtSQL, selectClause, tblname, l.geomField), nil
}

// mercatorLatLimit is the highest latitude EPSG:3857 can express: past it the
// mercator y goes to infinity, and log(tan(0)) at the south pole reaches it
// exactly rather than approaching it.
//
// A geographic tiling scheme runs to +-90, and its buffered extent runs past
// that -- WorldCRS84Quad z0 buffered by 64px is -92.8125..92.8125. Transforming
// those straight through produces -Inf, NaN, or 238107693.26 (the northern
// pole, which floating-point tan lands just short of infinity on), and all
// three reach PostgreSQL as an ST_MakeEnvelope argument: -Inf parses as the
// identifier "inf" and fails the query outright, while the finite one is a
// silently wrong envelope 11.9x too tall.
//
// Clamping is not an approximation here. This bound is only applied to an
// envelope in a mercator SRID, and a layer stored in one holds nothing outside
// it to select.
const mercatorLatLimit = 85.05112877980659

// webMercatorQuadZ0ScaleDenominator is WebMercatorQuad's zoom 0 scale
// denominator. TestWebMercatorQuadZ0ScaleDenominator pins it against the
// registry, which is the definition; it is repeated here so that deriving
// WebMercatorZoomToken costs no lookup on the request path.
const webMercatorQuadZ0ScaleDenominator = 559082264.0287178

// replaceTokens replaces tokens in the provided SQL string
//
// !BBOX! - the tile's envelope in the layer's SRID: what to select rows with
// !TILE_BBOX! - the tile's envelope in the tiling scheme's CRS: what to clip against
// !TILE_SRID! - the EPSG code of the tiling scheme's CRS, to transform the geometry to
// !ZOOM! - the tile Z value
// !X! - the tile X value
// !Y! - the tile Y value
// !Z! - the tile Z value
// !WEB_MERCATOR_ZOOM! - the WebMercatorQuad zoom of the same scale
// !SCALE_DENOMINATOR! - scale denominator, assuming 90.7 DPI (i.e. 0.28mm pixel size)
// !PIXEL_WIDTH! - the pixel width in meters
// !PIXEL_HEIGHT! - the pixel height in meters
// !GEOM_FIELD! - the geom field name
// !GEOM_TYPE! - the geom field type if defined otherwise ""
//
// !BBOX! and !TILE_BBOX! are the same envelope, and the same string, whenever
// the layer is stored in the scheme's own CRS. That was every request this
// server served for as long as WebMercatorQuad was the only scheme, which is
// why one token did both jobs. It cannot: an mvt_postgis layer hands the
// tile-space mapping to ST_AsMVTGeom, which spaces the tile by the axis of
// whatever CRS its envelope is in. Give it a mercator envelope for a
// WorldCRS84Quad tile and the tile is mercator-spaced inside a plate-carree
// frame -- at z1 that puts every feature between the equator and 85N into the
// bottom 8.4% of the tile (MAPCO-11614).
func replaceTokens(sql string, lyr *Layer, tile provider.Tile, withBuffer bool) (string, error) {
	var (
		extent   *geom.Extent
		geoType  string
		tileSRID uint64
	)

	if lyr == nil {
		return "", ErrNilLayer
	}
	srid := lyr.SRID()

	if withBuffer {
		extent, tileSRID = tile.BufferedExtent()
	} else {
		extent, tileSRID = tile.Extent()
	}

	// The scheme's own envelope, untransformed -- this is the one ST_AsMVTGeom
	// needs, and the one that needs no conversion to be right.
	tileBBox := envelopeSQL(extent.MinX(), extent.MinY(), extent.MaxX(), extent.MaxY(), tileSRID)

	bbox, err := layerEnvelopeSQL(extent, tileSRID, srid)
	if err != nil {
		return "", err
	}

	pixelWidth, pixelHeight, scaleDenominator, webMercatorZoom := tileScale(tile)

	if lyr.GeomType() != nil {
		geoType = fmt.Sprintf("%v", lyr.GeomType())
	}

	// replace query string tokens
	z, x, y := tile.ZXY()
	tokenReplacer := strings.NewReplacer(
		config.BboxToken, bbox,
		config.TileBboxToken, tileBBox,
		config.TileSridToken, strconv.FormatUint(tileSRID, 10),
		config.ZoomToken, strconv.FormatUint(uint64(z), 10),
		config.ZToken, strconv.FormatUint(uint64(z), 10),
		config.XToken, strconv.FormatUint(uint64(x), 10),
		config.YToken, strconv.FormatUint(uint64(y), 10),
		config.WebMercatorZoomToken, strconv.Itoa(webMercatorZoom),
		config.ScaleDenominatorToken, strconv.FormatFloat(scaleDenominator, 'f', 8, 64),
		config.PixelWidthToken, strconv.FormatFloat(pixelWidth, 'f', 8, 64),
		config.PixelHeightToken, strconv.FormatFloat(pixelHeight, 'f', 8, 64),
		config.IdFieldToken, lyr.IDFieldName(),
		config.GeomFieldToken, lyr.GeomFieldName(),
		config.GeomTypeToken, geoType,
	)

	uppercaseTokenSQL := uppercaseTokens(sql)

	return tokenReplacer.Replace(uppercaseTokenSQL), nil
}

// envelopeSQL renders an ST_MakeEnvelope call. The 8 decimal places are what
// callers have always emitted and what the tests pin.
func envelopeSQL(minX, minY, maxX, maxY float64, srid uint64) string {
	return fmt.Sprintf("ST_MakeEnvelope(%.8f,%.8f,%.8f,%.8f,%d)", minX, minY, maxX, maxY, srid)
}

// layerEnvelopeSQL converts a tile's envelope from the tiling scheme's CRS into
// the layer's SRID, so that the result can be compared against a column the
// spatial index covers.
//
// This is a selection envelope and nothing else. It is the wrong thing to clip
// a tile against whenever the two CRSs differ -- see replaceTokens.
func layerEnvelopeSQL(extent *geom.Extent, tileSRID, srid uint64) (string, error) {
	minX, minY := extent.MinX(), extent.MinY()
	maxX, maxY := extent.MaxX(), extent.MaxY()

	// basic.Transform routes everything through web mercator, so a latitude
	// outside the mercator range is unrepresentable on the way out and, for a
	// buffered geographic tile, on the way in as well.
	if tileSRID == shigola.WGS84 && srid != shigola.WGS84 {
		minY = math.Max(minY, -mercatorLatLimit)
		maxY = math.Min(maxY, mercatorLatLimit)
	}

	minGeo, err := basic.Transform(tileSRID, srid, geom.Point{minX, minY})
	if err != nil {
		return "", fmt.Errorf("Error trying to convert tile point: %w ", err)
	}

	maxGeo, err := basic.Transform(tileSRID, srid, geom.Point{maxX, maxY})
	if err != nil {
		return "", fmt.Errorf("Error trying to convert tile point: %w ", err)
	}

	minPt, maxPt := minGeo.(geom.Point), maxGeo.(geom.Point)

	return envelopeSQL(minPt.X(), minPt.Y(), maxPt.X(), maxPt.Y(), srid), nil
}

// tileScale reports a tile's resolution: pixel size in metres, scale
// denominator, and the WebMercatorQuad zoom of the same scale.
//
// All four come off the tiling scheme's own matrix rather than being divided
// out of the tile's extent. The extent is in the scheme's CRS, whose units are
// degrees for a geographic scheme -- dividing that by a tile width yields
// degrees per pixel, and the old code went on to treat the result as metres.
// The matrix states the answer in metres for every scheme, because that is what
// a scale denominator means.
//
// A tile with no resolvable grid or matrix falls back to the extent arithmetic:
// it is already an unserveable tile (Extent logs and returns an empty one), and
// a token value of zero says less about why than a wrong one does.
func tileScale(tile provider.Tile) (pixelWidth, pixelHeight, scaleDenominator float64, webMercatorZoom int) {
	z, _, _ := tile.ZXY()

	if grid := tile.Grid(); grid != nil {
		if m, err := grid.Matrix(int(z)); err == nil {
			pixelSize := m.CellSize * grid.MetersPerUnit()

			return pixelSize, pixelSize, m.ScaleDenominator,
				int(math.Round(math.Log2(webMercatorQuadZ0ScaleDenominator / m.ScaleDenominator)))
		}
	}

	extent, _ := tile.Extent()
	pixelWidth = (extent.MaxX() - extent.MinX()) / 256
	pixelHeight = (extent.MaxY() - extent.MinY()) / 256

	return pixelWidth, pixelHeight, pixelWidth / 0.00028 /* px size in m */, int(z)
}

// extractQueryParamValues finds default values for SQL tokens and constructs query parameter values out of them
func extractQueryParamValues(pname string, maps []provider.Map, layer *Layer) provider.Params {
	result := make(provider.Params, 0)

	expectedMapName := fmt.Sprintf("%s.%s", pname, layer.name)
	for _, m := range maps {
		for _, l := range m.Layers {
			if l.ProviderLayer == env.String(expectedMapName) {
				for _, p := range m.Parameters {
					pv, err := p.ToDefaultValue()
					if err == nil {
						result[p.Token] = pv
					}
				}
			}
		}
	}

	return result
}

// uppercaseTokens converts all !tokens! to uppercase !TOKENS!. Tokens can
// contain alphanumerics, dash and underline chars.
func uppercaseTokens(str string) string {
	return provider.ParameterTokenRegexp.ReplaceAllStringFunc(str, strings.ToUpper)
}

// ctxErr will check if the supplied context has an error (i.e. context canceled)
// and if so, return that error, else return the supplied error. This is useful
// as not all of Go's stdlib has adopted error wrapping so context.Canceled
// errors are not always easy to capture.
func ctxErr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	return err
}

// NOTE: @iwpnd to remove this adapter once we move to slog
// LoggerAdapter adapts the internal logger to the pgx tracelog.Logger interface.
type LoggerAdapter struct{}

func NewLoggerAdapter() *LoggerAdapter {
	return &LoggerAdapter{}
}

// Log is the implementation of the Log method required by pgx's tracelog.Logger interface.
// It logs messages with the warn level only.
func (l *LoggerAdapter) Log(
	ctx context.Context, level tracelog.LogLevel,
	msg string, data map[string]any,
) {
	// drop >3, where 2=Error 3=Warn
	if level > tracelog.LogLevelWarn {
		return
	}

	if level == tracelog.LogLevelError {
		log.Errorf("PostGIS(pgx): %s, %#v", msg, data)
	} else {
		log.Warnf("PostGIS(pgx): %s, %#v", msg, data)
	}
}
