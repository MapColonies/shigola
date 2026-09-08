package postgis

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/config"
	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/maths/webmercator"
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

// transformPoint reprojects a point between the two SRIDs a layer may declare,
// returning it unchanged when they match.
//
// This replaced basic.Transform, which MAPCO-11492 deleted along with the rest
// of the Go-side geometry trees. That function dispatched over every geometry
// type in order to walk one down to its points, but the only geometries it was
// ever handed here were the two BBOX corners in replaceTokens, so the walk was
// generality without a caller. The SRID pair is closed for the same reason
// config validation closes it: 3857 and 4326 are what a layer may declare.
func transformPoint(fromSRID, toSRID uint64, pt geom.Point) (geom.Point, error) {
	if fromSRID == toSRID {
		return pt, nil
	}

	var (
		crds []float64
		err  error
	)

	switch {
	case fromSRID == shigola.WGS84 && toSRID == shigola.WebMercator:
		crds, err = webmercator.PToXY(pt.X(), pt.Y())
	case fromSRID == shigola.WebMercator && toSRID == shigola.WGS84:
		crds, err = webmercator.PToLonLat(pt.X(), pt.Y())
	default:
		return geom.Point{}, fmt.Errorf("postgis: do not know how to convert from %v to %v", fromSRID, toSRID)
	}
	if err != nil {
		return geom.Point{}, err
	}

	return geom.Point{crds[0], crds[1]}, nil
}

// replaceTokens replaces tokens in the provided SQL string
//
// !BBOX! - the bounding box of the tile
// !ZOOM! - the tile Z value
// !X! - the tile X value
// !Y! - the tile Y value
// !Z! - the tile Z value
// !SCALE_DENOMINATOR! - scale denominator, assuming 90.7 DPI (i.e. 0.28mm pixel size)
// !PIXEL_WIDTH! - the pixel width in meters, assuming 256x256 tiles
// !PIXEL_HEIGHT! - the pixel height in meters, assuming 256x256 tiles
// !GEOM_FIELD! - the geom field name
// !GEOM_TYPE! - the geom field type if defined otherwise ""
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
	if tileSRID == shigola.WGS84 {
		z, x, y := tile.ZXY()
		log.Debugf("postgis: replacing tokens for WorldCRS84Quad tile z=%v x=%v y=%v tile_srid=%v layer_srid=%v with_buffer=%v extent=%v", z, x, y, tileSRID, srid, withBuffer, extent)
	}

	minPt, err := transformPoint(tileSRID, srid, geom.Point{extent.MinX(), extent.MinY()})
	if err != nil {
		return "", fmt.Errorf("postgis: converting tile point: %w", err)
	}

	maxPt, err := transformPoint(tileSRID, srid, geom.Point{extent.MaxX(), extent.MaxY()})
	if err != nil {
		return "", fmt.Errorf("postgis: converting tile point: %w", err)
	}

	bbox := fmt.Sprintf(
		"ST_MakeEnvelope(%.8f,%.8f,%.8f,%.8f,%d)",
		minPt.X(),
		minPt.Y(),
		maxPt.X(),
		maxPt.Y(),
		srid,
	)
	if tileSRID == shigola.WGS84 {
		log.Debugf("postgis: WorldCRS84Quad BBOX tile_srid=%v layer_srid=%v min=%v max=%v bbox=%v", tileSRID, srid, minPt, maxPt, bbox)
	}

	extent, _ = tile.Extent()
	// TODO: Always convert to meter if we support different projections
	pixelWidth := (extent.MaxX() - extent.MinX()) / 256
	pixelHeight := (extent.MaxY() - extent.MinY()) / 256
	scaleDenominator := pixelWidth / 0.00028 /* px size in m */

	if lyr.GeomType() != nil {
		geoType = fmt.Sprintf("%v", lyr.GeomType())
	}

	// replace query string tokens
	z, x, y := tile.ZXY()
	tokenReplacer := strings.NewReplacer(
		config.BboxToken, bbox,
		config.ZoomToken, strconv.FormatUint(uint64(z), 10),
		config.ZToken, strconv.FormatUint(uint64(z), 10),
		config.XToken, strconv.FormatUint(uint64(x), 10),
		config.YToken, strconv.FormatUint(uint64(y), 10),
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
