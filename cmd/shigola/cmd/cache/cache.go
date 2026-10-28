package cache

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MapColonies/shigola/config"
	"github.com/MapColonies/shigola/internal/cacheops"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/go-spatial/cobra" // The config from the main app
	"github.com/go-spatial/geom/slippy"
)

// Config from the main app
var Config *config.Config
var RequireCache bool

var Cmd = &cobra.Command{
	Use:   "cache",
	Short: "command to manage the cache",
	Long:  "command to manage the cache",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		RequireCache = true
		if cmd.HasParent() {
			// run the parents Persistent Run commands.
			pcmd := cmd.Parent()
			if pcmd.PersistentPreRunE != nil {
				if err := pcmd.PersistentPreRunE(pcmd, args); err != nil {
					return err
				}
			}
		}
		return nil
	},
}

func init() {
	Cmd.AddCommand(SeedPurgeCmd)
	Cmd.SetUsageTemplate(`Usage: {{.CommandPath}} [command]{{if .HasExample}}

Examples:
{{.Example}}{{end}}

Available Commands:
  {{rpad "seed" .NamePadding}} seed tiles to the cache
  {{rpad "purge" .NamePadding}} purge tiles from the cache{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}
Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}
Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}
Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`)
}

type TileChannel struct {
	channel  chan slippy.Tile
	cl       sync.RWMutex
	isClosed bool
	l        sync.RWMutex
	err      error
}

func (tc *TileChannel) Channel() <-chan slippy.Tile {
	if tc == nil {
		return nil
	}
	return tc.channel
}

func (tc *TileChannel) Err() (e error) {
	if tc == nil {
		return nil
	}
	tc.l.RLock()
	e = tc.err
	tc.l.RUnlock()
	return e
}

func (tc *TileChannel) setError(err error) {
	if tc == nil {
		return
	}
	tc.l.Lock()
	tc.err = err
	tc.l.Unlock()
}
func (tc *TileChannel) Close() {
	var isClosed bool
	tc.cl.RLock()
	isClosed = tc.isClosed
	tc.cl.RUnlock()
	if isClosed {
		return
	}
	tc.cl.Lock()
	if !tc.isClosed {
		tc.isClosed = true
		close(tc.channel)
	}
	tc.cl.Unlock()
}

// runSeedPurge runs the resolved seed or purge over tiles. If the workers are
// still busy 60 seconds after the last tile was handed out, the process exits.
// zooms, when the source covers a known set of them, lets a seed of a layer
// active at none be refused up front; nil skips that check.
func runSeedPurge(ctx context.Context, tiles *TileChannel, zooms []uint) error {
	for _, m := range seedPurgeMaps {
		if len(m.Params) > 0 {
			log.Warnf("caching is disabled for map %s as it has custom parameters configures", m.Name)
		}
	}

	var layers []string
	for _, l := range strings.Split(cacheLayers, ",") {
		if l = strings.TrimSpace(l); l != "" {
			layers = append(layers, l)
		}
	}

	targets := make([]cacheops.Target, len(seedPurgeMaps))
	for i, m := range seedPurgeMaps {
		targets[i] = cacheops.Target{Map: m, Layers: layers}
	}
	// Run validates the targets too, but they must pass before the zooms are
	// checked, so a misspelt layer is reported as such
	if err := cacheops.ValidateTargets(seedPurgeOp, targets); err != nil {
		return err
	}
	if zooms != nil {
		if err := cacheops.ValidateZooms(seedPurgeOp, targets, zooms); err != nil {
			return err
		}
	}

	res, err := cacheops.Run(ctx, cacheops.Options{
		Op:             seedPurgeOp,
		Targets:        targets,
		Grid:           seedPurgeGrid,
		Concurrency:    cacheConcurrency,
		Overwrite:      cacheOverwrite,
		WriteTiers:     seedWriteTiers,
		LogThreshold:   time.Duration(cacheLogThreshold) * time.Millisecond,
		CollectGarbage: true,
		Draining:       exitIfWorkersHang(60 * time.Second),
	}, tiles)
	log.Infof("cache %v: %d done, %d skipped", seedPurgeOp, res.Done, res.Skipped)

	return err
}

func exitIfWorkersHang(after time.Duration) func() func() {
	return func() func() {
		log.Info("waiting for workers to finish up")
		timer := time.AfterFunc(after, func() {
			log.Infof("%v passed killing", after)
			os.Exit(1)
		})

		return func() {
			timer.Stop()
			log.Info("all workers are done")
		}
	}
}

func IsValidLat(f64 float64) bool { return -90.0 <= f64 && f64 <= 90.0 }
func IsValidLatString(lat string) (float64, bool) {
	f64, err := strconv.ParseFloat(strings.TrimSpace(lat), 64)
	return f64, err == nil && IsValidLat(f64)
}

func IsValidLng(f64 float64) bool { return -180.0 <= f64 && f64 <= 180.0 }
func IsValidLngString(lng string) (float64, bool) {
	f64, err := strconv.ParseFloat(strings.TrimSpace(lng), 64)
	return f64, err == nil && IsValidLng(f64)
}

func sliceFromRange(min, max uint) ([]uint, error) {
	var ret []uint
	if max < min {
		return nil, fmt.Errorf("min (%v) is greater than max (%v)", min, max)
	}

	ret = make([]uint, max-min+1)
	for i := min; i <= max; i++ {
		ret[i-min] = i
	}

	return ret, nil
}
