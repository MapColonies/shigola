package atlas

import (
	"github.com/go-spatial/geom"
)

type Layer struct {
	// optional. if not set, the ProviderLayerName will be used
	Name              string
	ProviderLayerName string
	MinZoom           uint
	MaxZoom           uint
	GeomType          geom.Geometry
}

// MVTName will return the value that will be encoded in the Name field when the layer is encoded as MVT
func (l *Layer) MVTName() string {
	if l.Name != "" {
		return l.Name
	}

	return l.ProviderLayerName
}
