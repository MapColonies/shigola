package atlas

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/MapColonies/shigola/observability"

	"github.com/golang/protobuf/proto"

	"github.com/MapColonies/shigola"
	"github.com/MapColonies/shigola/basic"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/convert"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/maths/simplify"
	"github.com/MapColonies/shigola/maths/validate"
	"github.com/MapColonies/shigola/provider"
	"github.com/MapColonies/shigola/provider/debug"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom"
	"github.com/go-spatial/geom/cmp"
	"github.com/go-spatial/geom/encoding/mvt"
	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/geom/winding"
)

// NewWebMercatorMap creates a new map with the necessary default values
func NewWebMercatorMap(name string) Map {
	return Map{
		Name: name,
		// default bounds
		Bounds:         shigola.WGS84Bounds,
		Layers:         []Layer{},
		SRID:           shigola.WebMercator,
		TileMatrixSets: []*tms.TileMatrixSet{DefaultTileGrid()},
		TileExtent:     4096,
		TileBuffer:     uint64(shigola.DefaultTileBuffer),
	}
}

// DefaultTileGrid is the TileMatrixSet a map serves when its configuration names
// none: WebMercatorQuad, the grid tegola has always served.
//
// WebMercatorQuad is bundled and active in every build, so a failure to resolve
// it means the tms package's embedded definitions are broken — the same
// condition its own init panics on.
func DefaultTileGrid() *tms.TileMatrixSet {
	grid, err := tms.Get(tms.WebMercatorQuad)
	if err != nil {
		panic("atlas: default tile grid " + tms.WebMercatorQuad + " is unavailable: " + err.Error())
	}

	return grid
}

// Map defines a Web Mercator map
type Map struct {
	Name string
	// Contains an attribution to be displayed when the map is shown to a user.
	// 	This string is sanitized so it can't be abused as a vector for XSS or beacon tracking.
	Attribution string
	// The maximum extent of available map tiles in WGS:84
	// latitude and longitude values, in the order left, bottom, right, top.
	// Default: [-180, -85, 180, 85]
	Bounds *geom.Extent
	// The first value is the longitude, the second is latitude (both in
	// WGS:84 values), the third value is the zoom level.
	Center [3]float64
	Layers []Layer
	// Params holds configured query parameters
	Params []provider.QueryParameter

	SRID uint64
	// TileMatrixSets are the grids this map may be requested in. The first is
	// the map's default: the grid its native /maps/... routes serve, and the
	// one an OGC request that does not name a grid gets (ADR-0008).
	//
	// An empty list means the default grid alone; read it through TileGrid and
	// TileGrids rather than directly.
	TileMatrixSets []*tms.TileMatrixSet
	// MVT output values
	TileExtent uint64
	TileBuffer uint64

	mvtProviderName string
	mvtProvider     provider.MVTTiler

	observer observability.Interface
}

// TileGrid returns the map's default TileMatrixSet — the grid its tiles are cut
// in unless a request names another. It is never nil.
func (m Map) TileGrid() *tms.TileMatrixSet {
	for _, grid := range m.TileMatrixSets {
		if grid != nil {
			return grid
		}
	}

	return DefaultTileGrid()
}

// TileGrids returns every TileMatrixSet this map may be requested in, defaulting
// to the default grid alone. The first entry is the map's default.
func (m Map) TileGrids() []*tms.TileMatrixSet {
	grids := make([]*tms.TileMatrixSet, 0, len(m.TileMatrixSets))
	for _, grid := range m.TileMatrixSets {
		if grid != nil {
			grids = append(grids, grid)
		}
	}

	if len(grids) == 0 {
		return []*tms.TileMatrixSet{DefaultTileGrid()}
	}

	return grids
}

// SupportsTileGrid reports whether this map may be requested in the named grid.
func (m Map) SupportsTileGrid(id string) bool {
	for _, grid := range m.TileGrids() {
		if grid.ID() == id {
			return true
		}
	}

	return false
}

func prepareGeometryForMVT(geo geom.Geometry, tileExtent *geom.Extent, pixelExtent float64) geom.Geometry {
	switch g := geo.(type) {
	case geom.Polygon:
		return preparePolygonForMVT(g, tileExtent, pixelExtent)
	case geom.MultiPolygon:
		polys := g.Polygons()
		prepared := make(geom.MultiPolygon, 0, len(polys))
		for _, poly := range polys {
			pp := preparePolygonForMVT(geom.Polygon(poly), tileExtent, pixelExtent)
			if len(pp) > 0 {
				prepared = append(prepared, pp)
			}
		}
		if len(prepared) == 0 {
			return nil
		}
		return prepared
	default:
		return mvt.PrepareGeo(geo, tileExtent, pixelExtent)
	}
}

func preparePolygonForMVT(poly geom.Polygon, tileExtent *geom.Extent, pixelExtent float64) geom.Polygon {
	rings := poly.LinearRings()
	prepared := make(geom.Polygon, 0, len(rings))
	for _, ring := range rings {
		pr := prepareRingForMVT(ring, tileExtent, pixelExtent)
		if len(pr) >= 3 {
			prepared = append(prepared, pr)
		}
	}
	if len(prepared) == 0 {
		return nil
	}

	order := winding.Order{YPositiveDown: false}
	return geom.Polygon(order.RectifyPolygon([][][2]float64(prepared)))
}

func prepareRingForMVT(ring [][2]float64, tileExtent *geom.Extent, pixelExtent float64) [][2]float64 {
	if len(ring) < 3 {
		return nil
	}
	prepared := make([][2]float64, 0, len(ring))
	for _, pt := range ring {
		npt := preparePointForMVT(pt, tileExtent, pixelExtent)
		if len(prepared) > 0 && sameMVTPoint(prepared[len(prepared)-1], npt) {
			continue
		}
		prepared = append(prepared, npt)
	}
	if len(prepared) > 1 && sameMVTPoint(prepared[0], prepared[len(prepared)-1]) {
		prepared = prepared[:len(prepared)-1]
	}
	if len(prepared) < 3 {
		return nil
	}
	return prepared
}

func preparePointForMVT(pt [2]float64, tileExtent *geom.Extent, pixelExtent float64) [2]float64 {
	return [2]float64{
		(pt[0] - tileExtent.MinX()) / tileExtent.XSpan() * pixelExtent,
		(tileExtent.MaxY() - pt[1]) / tileExtent.YSpan() * pixelExtent,
	}
}

func sameMVTPoint(a, b [2]float64) bool {
	return int64(a[0]) == int64(b[0]) && int64(a[1]) == int64(b[1]) ||
		cmp.HiCMP.PointEqual(a, b)
}

func pixelBufferedExtent(buffer uint64) *geom.Extent {
	b := float64(buffer)
	extent := float64(mvt.DefaultExtent)
	return geom.NewExtent([2]float64{-b, -b}, [2]float64{extent + b, extent + b})
}

func clipGeometryToExtent(geo geom.Geometry, extent *geom.Extent) geom.Geometry {
	switch g := geo.(type) {
	case geom.Polygon:
		return clipPolygonToExtent(g, extent)
	case geom.MultiPolygon:
		polys := g.Polygons()
		clipped := make(geom.MultiPolygon, 0, len(polys))
		for _, poly := range polys {
			cp := clipPolygonToExtent(geom.Polygon(poly), extent)
			if len(cp) > 0 {
				clipped = append(clipped, cp)
			}
		}
		if len(clipped) == 0 {
			return nil
		}
		return clipped
	default:
		return geo
	}
}

func clipPolygonToExtent(poly geom.Polygon, extent *geom.Extent) geom.Polygon {
	rings := poly.LinearRings()
	clipped := make(geom.Polygon, 0, len(rings))
	for _, ring := range rings {
		cr := clipRingToExtent(ring, extent)
		if len(cr) >= 4 {
			clipped = append(clipped, cr)
		}
	}
	if len(clipped) == 0 {
		return nil
	}
	return clipped
}

type clipEdge uint8

const (
	clipLeft clipEdge = iota
	clipRight
	clipBottom
	clipTop
)

func clipRingToExtent(ring [][2]float64, extent *geom.Extent) [][2]float64 {
	pts := openRing(ring)
	for _, edge := range []clipEdge{clipLeft, clipRight, clipBottom, clipTop} {
		pts = clipRingToEdge(pts, extent, edge)
		if len(pts) == 0 {
			return nil
		}
	}
	if len(pts) < 3 {
		return nil
	}
	return closeRing(pts)
}

func openRing(ring [][2]float64) [][2]float64 {
	if len(ring) == 0 {
		return nil
	}
	pts := append([][2]float64(nil), ring...)
	last := len(pts) - 1
	if pts[0] == pts[last] {
		pts = pts[:last]
	}
	return pts
}

func closeRing(ring [][2]float64) [][2]float64 {
	if len(ring) == 0 {
		return nil
	}
	if ring[0] != ring[len(ring)-1] {
		ring = append(ring, ring[0])
	}
	return ring
}

func clipRingToEdge(ring [][2]float64, extent *geom.Extent, edge clipEdge) [][2]float64 {
	if len(ring) == 0 {
		return nil
	}
	out := make([][2]float64, 0, len(ring)+1)
	prev := ring[len(ring)-1]
	prevInside := pointInsideEdge(prev, extent, edge)
	for _, curr := range ring {
		currInside := pointInsideEdge(curr, extent, edge)
		switch {
		case currInside && !prevInside:
			out = append(out, intersectClipEdge(prev, curr, extent, edge), curr)
		case currInside && prevInside:
			out = append(out, curr)
		case !currInside && prevInside:
			out = append(out, intersectClipEdge(prev, curr, extent, edge))
		}
		prev = curr
		prevInside = currInside
	}
	return out
}

func pointInsideEdge(pt [2]float64, extent *geom.Extent, edge clipEdge) bool {
	switch edge {
	case clipLeft:
		return pt[0] >= extent.MinX()
	case clipRight:
		return pt[0] <= extent.MaxX()
	case clipBottom:
		return pt[1] >= extent.MinY()
	case clipTop:
		return pt[1] <= extent.MaxY()
	default:
		return false
	}
}

func intersectClipEdge(a, b [2]float64, extent *geom.Extent, edge clipEdge) [2]float64 {
	dx := b[0] - a[0]
	dy := b[1] - a[1]
	switch edge {
	case clipLeft:
		return intersectVertical(a, dx, dy, extent.MinX())
	case clipRight:
		return intersectVertical(a, dx, dy, extent.MaxX())
	case clipBottom:
		return intersectHorizontal(a, dx, dy, extent.MinY())
	case clipTop:
		return intersectHorizontal(a, dx, dy, extent.MaxY())
	default:
		return b
	}
}

func intersectVertical(a [2]float64, dx, dy, x float64) [2]float64 {
	if dx == 0 {
		return [2]float64{x, a[1]}
	}
	t := (x - a[0]) / dx
	return [2]float64{x, a[1] + t*dy}
}

func intersectHorizontal(a [2]float64, dx, dy, y float64) [2]float64 {
	if dy == 0 {
		return [2]float64{a[0], y}
	}
	t := (y - a[1]) / dy
	return [2]float64{a[0] + t*dx, y}
}

// HasMVTProvider indicates if map is a mvt provider based map
func (m Map) HasMVTProvider() bool { return m.mvtProvider != nil }

// MVTProvider returns the mvt provider if this map is a mvt provider based map, otherwise nil
func (m Map) MVTProvider() provider.MVTTiler { return m.mvtProvider }

// MVTProviderName returns the mvt provider name if this map is a mvt provider based map, otherwise ""
func (m Map) MVTProviderName() string { return m.mvtProviderName }

// SetMVTProvider sets the map to be based on the passed in mvt provider, and returning the provider
func (m *Map) SetMVTProvider(name string, p provider.MVTTiler) provider.MVTTiler {
	m.mvtProviderName = name
	m.mvtProvider = p
	return p
}

func (m Map) Collectors(prefix string, config func(configKey string) map[string]interface{}) ([]observability.Collector, error) {
	if m.mvtProviderName != "" {
		collect, ok := m.mvtProvider.(observability.Observer)
		if !ok {
			return nil, nil
		}
		return collect.Collectors(prefix, config)
	}
	// not an mvtProvider, so need to ask each layer instead
	var collection []observability.Collector
	for i := range m.Layers {
		aCollection, err := m.Layers[i].Collectors(prefix, config)
		if err != nil {
			return nil, err
		}
		if len(aCollection) != 0 {
			collection = append(collection, aCollection...)
		}
	}
	return collection, nil
}

// AddDebugLayers returns a copy of a Map with the debug layers appended to the layer list
func (m Map) AddDebugLayers() Map {
	// can not modify the layers of an mvt provider based map
	if m.mvtProvider != nil {
		return m
	}

	// make an explicit copy of the layers
	layers := make([]Layer, len(m.Layers))
	copy(layers, m.Layers)
	m.Layers = layers

	// setup a debug provider
	debugProvider, _ := debug.NewTileProvider(dict.Dict{}, nil)

	m.Layers = append(layers, []Layer{
		{
			Name:              debug.LayerDebugTileOutline,
			ProviderLayerName: debug.LayerDebugTileOutline,
			Provider:          debugProvider,
			GeomType:          geom.LineString{},
			MinZoom:           0,
			MaxZoom:           MaxZoom,
		},
		{
			Name:              debug.LayerDebugTileCenter,
			ProviderLayerName: debug.LayerDebugTileCenter,
			Provider:          debugProvider,
			GeomType:          geom.Point{},
			MinZoom:           0,
			MaxZoom:           MaxZoom,
		},
	}...)

	return m
}

// FilterLayersByZoom returns a copy of a Map with a subset of layers that match the given zoom
func (m Map) FilterLayersByZoom(zoom slippy.Zoom) Map {
	var layers []Layer

	for i := range m.Layers {
		if slippy.Zoom(m.Layers[i].MinZoom) <= zoom && slippy.Zoom(m.Layers[i].MaxZoom) >= zoom {
			layers = append(layers, m.Layers[i])
			continue
		}
	}

	// overwrite the Map's layers with our subset
	m.Layers = layers

	return m
}

// FilterLayersByName returns a copy of a Map with a subset of layers that match the supplied list of layer names
func (m Map) FilterLayersByName(names ...string) Map {
	var layers []Layer

	nameStr := strings.Join(names, ",")
	for i := range m.Layers {
		// if we have a name set, use it for the lookup
		if m.Layers[i].Name != "" && nameStr == m.Layers[i].Name {
			layers = append(layers, m.Layers[i])
			continue
		} else if m.Layers[i].ProviderLayerName != "" && strings.Contains(nameStr, m.Layers[i].ProviderLayerName) { // default to using the ProviderLayerName for the lookup
			layers = append(layers, m.Layers[i])
			continue
		}
	}

	// overwrite the Map's layers with our subset
	m.Layers = layers

	return m
}

func (m Map) encodeMVTProviderTile(ctx context.Context, tile slippy.Tile, params provider.Params) ([]byte, error) {
	// get the list of our layers
	ptile := provider.NewTileForGrid(tile.Z, tile.X, tile.Y, uint(m.TileBuffer), m.TileGrid())

	layers := make([]provider.Layer, len(m.Layers))
	for i := range m.Layers {
		layers[i] = provider.Layer{
			Name:    m.Layers[i].ProviderLayerName,
			MVTName: m.Layers[i].MVTName(),
		}
	}
	return m.mvtProvider.MVTForLayers(ctx, ptile, params, layers)

}

// encodeMVTTile will encode the given tile into mvt format
// TODO (arolek): support for max zoom
func (m Map) encodeMVTTile(ctx context.Context, tile slippy.Tile, params provider.Params) ([]byte, error) {

	// tile container
	var mvtTile mvt.Tile
	// wait group for concurrent layer fetching
	var wg sync.WaitGroup

	// layer stack
	mvtLayers := make([]*mvt.Layer, len(m.Layers))

	// the grid this tile is cut in, and the CRS features must be reprojected
	// into before they are clipped and encoded. Both are properties of the map,
	// not of a layer, so they are resolved once here rather than per layer.
	grid := m.TileGrid()
	tileSRID, sridErr := grid.NativeSRID()
	if sridErr != nil {
		return nil, fmt.Errorf("tile grid %v has no SRID to encode in: %w", grid.ID(), sridErr)
	}

	// set our WaitGroup count
	wg.Add(len(m.Layers))

	// iterate our layers
	for i, layer := range m.Layers {

		// go routine for fetching the layer concurrently
		go func(i int, l Layer) {
			mvtLayer := mvt.Layer{
				Name: l.MVTName(),
			}

			// on completion let the wait group know
			defer wg.Done()

			ptile := provider.NewTileForGrid(tile.Z, tile.X, tile.Y,
				uint(m.TileBuffer), grid)

			// fetch layer from data provider
			err := l.Provider.TileFeatures(ctx, l.ProviderLayerName, ptile, params, func(f *provider.Feature) error {
				// skip row if geometry collection empty.
				g, ok := f.Geometry.(geom.Collection)
				if ok && len(g.Geometries()) == 0 {
					return nil
				}

				geo := f.Geometry

				// check if the feature SRID and tile SRID are different. If they are then reproject.
				if f.SRID != tileSRID {
					g, err := basic.Transform(f.SRID, tileSRID, geo)
					if err != nil {
						return fmt.Errorf("unable to transform geometry from SRID (%v) to tile SRID (%v) for feature %v due to error: %w", f.SRID, tileSRID, f.ID, err)
					}
					geo = g
				}

				// TODO: remove this geom conversion step once the simplify function uses geom types
				tegolaGeo, err := convert.ToTegola(geo)
				if err != nil {
					return err
				}

				// add default tags, but don't overwrite a tag that already exists
				for k, v := range l.DefaultTags {
					if _, ok := f.Tags[k]; !ok {
						f.Tags[k] = v
					}
				}

				sg := tegolaGeo
				// multiple ways to turn off simplification. check the atlas init() function
				// for how the second two conditions are set
				if !l.DontSimplify && simplifyGeometries && tile.Z < slippy.Zoom(simplificationMaxZoom) {
					// TODO (arolek): change out the tile type for VTile. shigola.Tile will be deprecated
					tegolaTile := shigola.TileFromSlippyTile(tile)
					sg = simplify.SimplifyGeometry(tegolaGeo, tegolaTile.ZEpislon())
				}

				// check if we need to clip and if we do build the clip region (tile extent)
				var clipRegion *geom.Extent
				if !l.DontClip {
					// CleanGeometry is expecting to operate in pixel coordinates so the clipRegion
					// will need to be in this same coordinate system. this will change when the new
					// make valid routing is implemented
					clipRegion = pixelBufferedExtent(m.TileBuffer)
				}

				// TODO: remove this geom conversion step once the simplify function uses geom types
				geo, err = convert.ToGeom(sg)
				if err != nil {
					return err
				}

				if !l.DontClip {
					clipExtent, _ := ptile.BufferedExtent()
					geo = clipGeometryToExtent(geo, clipExtent)
					if geo == nil {
						return nil
					}
				}

				// TODO(arolek): currently the validate.CleanGeometry method does not operate
				// well on geometries that are not scaled to tile coordinate space. this will change
				// with the adoption of the new make valid routine. once implemented, the clipRegion
				// calculation will need to be in the same coordinate space as the geometry the
				// make valid function will be operating on.
				ext, _ := ptile.Extent()
				geo = prepareGeometryForMVT(geo, ext, float64(mvt.DefaultExtent))
				if geo == nil {
					return nil
				}

				// TODO: remove this geom conversion step once the validate function uses geom types
				sg, err = convert.ToTegola(geo)
				if err != nil {
					return err
				}

				if !l.DontClean {
					tegolaGeo, err = validate.CleanGeometry(ctx, sg, clipRegion)
					if err != nil {
						return fmt.Errorf("err making geometry valid: %w", err)
					}
				} else {
					tegolaGeo = sg
				}

				geo, err = convert.ToGeom(tegolaGeo)
				if err != nil {
					return nil
				}

				mvtLayer.AddFeatures(mvt.Feature{
					ID:       &f.ID,
					Tags:     f.Tags,
					Geometry: geo,
				})

				return nil
			})
			if err != nil {
				switch {
				case errors.Is(err, context.Canceled):
					// Do nothing if we were cancelled.

				// the underlying net.Dial function is not properly reporting
				// context.Canceled errors. Because of this, a string check on the error is performed.
				// there's an open issue for this and it appears it will be fixed eventually
				// but for now we have this check to avoid unnecessary logs
				// https://github.com/golang/go/issues/36208
				case strings.Contains(err.Error(), "operation was canceled"):
					// Do nothing, context was canceled

				default:
					// TODO (arolek): should we return an error to the response or just log the error?
					// we can't just write to the response as the WaitGroup is going to write to the response as well
					log.Errorf("err fetching tile (%v) features: %v", tile, err)
				}
				return
			}

			// add the layer to the slice position
			mvtLayers[i] = &mvtLayer
		}(i, layer)
	}

	// wait for the WaitGroup to finish
	wg.Wait()

	// stop processing if the context has an error. this check is necessary
	// otherwise the server continues processing even if the request was canceled
	// as the WaitGroup was not notified of the cancel
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// add layers to our tile
	err := mvtTile.AddLayers(mvtLayers...)
	if err != nil {
		return nil, err
	}

	// generate the MVT tile
	vtile, err := mvtTile.VTile(ctx)
	if err != nil {
		return nil, err
	}

	// encode our mvt tile
	return proto.Marshal(vtile)
}

// Encode will encode the given tile into mvt format
func (m Map) Encode(ctx context.Context, tile slippy.Tile, params provider.Params) ([]byte, error) {
	var (
		tileBytes []byte
		err       error
	)
	if m.HasMVTProvider() {
		tileBytes, err = m.encodeMVTProviderTile(ctx, tile, params)
	} else {
		tileBytes, err = m.encodeMVTTile(ctx, tile, params)
	}
	if err != nil {
		return nil, err
	}

	// buffer to store our compressed bytes
	var gzipBuf bytes.Buffer

	// compress the encoded bytes
	w := gzip.NewWriter(&gzipBuf)
	_, err = w.Write(tileBytes)
	if err != nil {
		return nil, err
	}

	// flush and close the writer
	if err = w.Close(); err != nil {
		return nil, err
	}

	// return encoded, gzipped tile
	return gzipBuf.Bytes(), nil
}
