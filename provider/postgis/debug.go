package postgis

import (
	"strings"

	"github.com/MapColonies/shigola/internal/env"
)

// debug determines weather extra debugging output is enabled.
// change debug to true to enable additional debugging output
// for this package
const debug = false

const (
	EnvSQLDebugName    = env.Prefix + "SQL_DEBUG"
	EnvSQLDebugLayer   = "LAYER_SQL"
	EnvSQLDebugExecute = "EXECUTE_SQL"
)

var (
	debugLayerSQL   bool
	debugExecuteSQL bool
)

func init() {
	debugLayerSQL, debugExecuteSQL = sqlDebugFlags()
}

// sqlDebugFlags reads EnvSQLDebugName through the env shim, so the pre-rename
// TEGOLA_SQL_DEBUG keeps working and says it is deprecated. It read the legacy
// name directly until MAPCO-11504, which left the documented name inert.
func sqlDebugFlags() (layer, execute bool) {
	v := env.Getenv(EnvSQLDebugName)
	return strings.Contains(v, EnvSQLDebugLayer), strings.Contains(v, EnvSQLDebugExecute)
}
