package register

import (
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/p"
	"github.com/MapColonies/shigola/observability"
)

func Observer(config dict.Dicter) (observability.Interface, error) {
	var oType = "none"
	if config != nil {
		oType, _ = config.String("type", p.String("none"))
	}
	return observability.For(oType, config)
}
