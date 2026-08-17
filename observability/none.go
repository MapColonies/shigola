package observability

import (
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/observer"
)

var NullObserver observer.Null

func noneInit(dict.Dicter) (Interface, error) { return NullObserver, nil }
