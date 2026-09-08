package register

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/config"
	"github.com/MapColonies/shigola/provider"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/geom"
)

func webMercatorMapFromConfigMap(cfg provider.Map) (newMap atlas.Map, err error) {
	newMap = atlas.NewWebMercatorMap(string(cfg.Name))
	newMap.Attribution = SanitizeAttribution(string(cfg.Attribution))
	newMap.Params = cfg.Parameters

	// convert from env package
	for i, v := range cfg.Center {
		newMap.Center[i] = float64(v)
	}

	if len(cfg.Bounds) == 4 {
		newMap.Bounds = geom.NewExtent(
			[2]float64{float64(cfg.Bounds[0]), float64(cfg.Bounds[1])},
			[2]float64{float64(cfg.Bounds[2]), float64(cfg.Bounds[3])},
		)
	}

	if cfg.TileBuffer != nil {
		newMap.TileBuffer = uint64(*cfg.TileBuffer)
	}

	// Carried across as a pointer rather than resolved to a bool here, so the
	// atlas map keeps the same three states the config has and nothing
	// downstream has to know which of them produced a true.
	if cfg.ServeLayerCollections != nil {
		serve := bool(*cfg.ServeLayerCollections)
		newMap.ServeLayerCollections = &serve
	}

	// A map that names no tiling schemes may be requested in any this build can
	// serve. Filling the list here rather than leaving it empty is what keeps
	// "no schemes named" from meaning "no schemes offered" downstream: nothing
	// past this point re-reads the config to find out which it was.
	//
	// AvailableIDs puts WebMercatorQuad first, which `cache seed --map` takes as
	// the run's scheme. Serving reads no default off the order — see
	// atlas.Map.TileMatrixSets.
	ids := make([]string, 0, len(cfg.TileMatrixSets))
	for _, id := range cfg.TileMatrixSets {
		ids = append(ids, string(id))
	}
	if len(ids) == 0 {
		ids = tms.AvailableIDs()
	}

	grids := make([]*tms.TileMatrixSet, 0, len(ids))
	for _, id := range ids {
		grid, err := tms.Get(id)
		if err != nil {
			return newMap, fmt.Errorf("map %v: tile matrix set %v: %w", cfg.Name, id, err)
		}
		grids = append(grids, grid)
	}
	newMap.TileMatrixSets = grids

	return newMap, nil
}

func layerInfosFindByName(infos []provider.LayerInfo, name string) provider.LayerInfo {
	if len(infos) == 0 {
		return nil
	}
	for i := range infos {
		if infos[i].Name() == name {
			return infos[i]
		}
	}
	return nil
}

func atlasLayerFromConfigLayer(cfg *provider.MapLayer, mapName string, layerProvider provider.Layerer) (layer atlas.Layer, err error) {
	var (
		// providerLayer is primary used for error reporting.
		providerLayer = string(cfg.ProviderLayer)
	)
	// read the provider's layer names
	// don't care about the error.
	providerName, layerName, _ := cfg.ProviderLayerName()
	layerInfos, err := layerProvider.Layers()
	if err != nil {
		return layer, ErrFetchingLayerInfo{
			Provider: providerName,
			Err:      err,
		}
	}
	layerInfo := layerInfosFindByName(layerInfos, layerName)
	if layerInfo == nil {
		return layer, ErrProviderLayerNotRegistered{
			MapName:       mapName,
			ProviderLayer: providerLayer,
			Provider:      providerName,
		}
	}
	layer.GeomType = layerInfo.GeomType()

	layer.Name = string(cfg.Name)
	layer.ProviderLayerName = layerName

	if cfg.MinZoom != nil {
		layer.MinZoom = uint(*cfg.MinZoom)
	}
	if cfg.MaxZoom != nil {
		layer.MaxZoom = uint(*cfg.MaxZoom)
	}
	return layer, nil
}

func selectProvider(name string, newMap *atlas.Map, providers map[string]provider.MVTTiler) (provider.Layerer, error) {
	if newMap.HasMVTProvider() {
		if newMap.MVTProviderName() != name {
			return nil, config.ErrMVTDifferentProviders{
				Original: newMap.MVTProviderName(),
				Current:  name,
			}
		}
		return newMap.MVTProvider(), nil
	}

	prvd, ok := providers[name]
	if !ok {
		return nil, ErrProviderNotFound{name}
	}

	// No check that the map is still empty: a map takes its provider from its
	// first layer, and every later layer goes through the branch above. Mixing
	// is caught in config.Validate, which sees all the layers at once.
	return newMap.SetMVTProvider(name, prvd), nil
}

// Maps registers maps with with atlas
func Maps(a *atlas.Atlas, maps []provider.Map, providers map[string]provider.MVTTiler) error {

	var (
		layerer provider.Layerer
	)

	// iterate our maps
	for _, m := range maps {
		newMap, err := webMercatorMapFromConfigMap(m)
		if err != nil {
			return err
		}

		// iterate our layers
		for _, l := range m.Layers {
			providerName, _, err := l.ProviderLayerName()
			if err != nil {
				return ErrProviderLayerInvalid{
					ProviderLayer: string(l.ProviderLayer),
					Map:           string(m.Name),
				}
			}

			// find our layer provider
			layerer, err = selectProvider(providerName, &newMap, providers)
			if err != nil {
				return err
			}

			layer, err := atlasLayerFromConfigLayer(&l, string(m.Name), layerer)
			if err != nil {
				return err
			}
			newMap.Layers = append(newMap.Layers, layer)
		}

		a.AddMap(newMap)
	}
	return nil
}

// Find allow HTML tag
var allowTags = regexp.MustCompile(`&lt;(a\s(.+?)|/a)&gt;`)

// Escapes HTML special characters except allow tags
func SanitizeAttribution(attribution string) string {
	result := html.EscapeString(attribution)
	tags := allowTags.FindAllString(result, -1)
	for _, tag := range tags {
		result = strings.Replace(result, tag, html.UnescapeString(tag), 1)
	}
	return result
}
