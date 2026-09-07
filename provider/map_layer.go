package provider

import (
	"fmt"
	"strings"

	"github.com/MapColonies/shigola/internal/env"
)

// MapLayer represents a the config for a layer in a map
type MapLayer struct {
	// Name is optional. If it's not defined the name of the ProviderLayer will be used.
	// Name can also be used to group multiple ProviderLayers under the same namespace.
	Name          env.String `toml:"name"`
	ProviderLayer env.String `toml:"provider_layer"`
	MinZoom       *env.Uint  `toml:"min_zoom"`
	MaxZoom       *env.Uint  `toml:"max_zoom"`
}

// RemovedMapLayerKeys are per-layer config keys this build no longer honours.
//
// They switched off, or fed, a step of the Go-side encode path -- simplify, clip,
// make-valid, tag -- which ran over features a standard provider handed back. That
// path is gone (MAPCO-11491), and an MVT provider returns a tile that PostGIS
// has already simplified and clipped, so there is no step left to switch off.
//
// They are listed rather than simply dropped because an unknown TOML key is
// silently ignored: a config still setting one would otherwise keep loading
// while quietly meaning nothing, which is the failure this list exists to
// prevent. Unlike a removed provider type there is no replacement to name --
// the honest instruction is to delete the line.
var RemovedMapLayerKeys = []string{
	"dont_simplify",
	"dont_clip",
	"dont_clean",
	// default_tags merged tags into each feature as it was encoded, which is
	// the same step, in the same loop, as the three above.
	"default_tags",
}

// ProviderLayerName returns the names of the layer and provider or an error
func (ml MapLayer) ProviderLayerName() (provider, layer string, err error) {
	// split the provider layer (syntax is provider.layer)
	plParts := strings.Split(string(ml.ProviderLayer), ".")
	if len(plParts) != 2 {
		// TODO (beymak): Properly handle the error
		return "", "", fmt.Errorf("config: invalid provider layer name (%v)", ml.ProviderLayer)
		// return "", "", ErrInvalidProviderLayerName{ProviderLayerName: string(ml.ProviderLayer)}
	}
	return plParts[0], plParts[1], nil
}

// GetName will return the user-defined Layer name from the config,
// or if the name is empty, return the name of the layer associated with
// the provider
func (ml MapLayer) GetName() (string, error) {
	if ml.Name != "" {
		return string(ml.Name), nil
	}
	_, name, err := ml.ProviderLayerName()
	return name, err
}
