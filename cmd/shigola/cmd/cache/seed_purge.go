package cache

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/cache"
	"github.com/MapColonies/shigola/cache/multi"
	"github.com/MapColonies/shigola/internal/build"
	gdcmd "github.com/MapColonies/shigola/internal/cmd"
	"github.com/MapColonies/shigola/internal/log"
	"github.com/MapColonies/shigola/observability"
	"github.com/MapColonies/shigola/provider"
	"github.com/MapColonies/shigola/tms"
	"github.com/go-spatial/cobra"
	"github.com/go-spatial/geom/slippy"
	"github.com/go-spatial/proj"
)

const defaultUsage = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
  {{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}
Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}
Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}
Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

// flag parameters
var (
	// cacheConcurrency is the amount of concurrency to use. defaults to the number of CPUs on the machine
	cacheConcurrency int
	// cacheOverwrite determines if we should overwrite already existing files or skip them
	cacheOverwrite bool
	// cacheBounds is the bounds that the cache is within. defaults to -180, -85.0511, 180, 85.0511
	cacheBounds string
	// cacheBoundsSRID is the srid of grid system that the bounds is at. Should Default to 4326
	cacheBoundsSRID int
	// cacheMap is the name of the map
	cacheMap string
	// cacheLogThreshold is cache threshold while seeding, to log output for tiles that take longer than this (in milliseconds) to render
	cacheLogThreshold int64
	// cacheTiers names the tiers of a layered cache that this run may write.
	// Empty means the last tier in read order; "all" means every tier.
	cacheTiers string
	// cacheTileMatrixSet names the tiling scheme this run enumerates and
	// writes. Empty means the default described by resolveSeedPurgeGrid.
	cacheTileMatrixSet string
)

// CacheTiersAll is the --cache-tiers value that lifts the restriction entirely.
const CacheTiersAll = "all"

// variables that are not flags but set by the command.
var (
	seedPurgeWorker func(context.Context, MapTile) error
	seedPurgeBounds [4]float64
	seedPurgeMaps   []atlas.Map
	// seedPurgeGrid is the TileMatrixSet this run enumerates tiles in and keys
	// them by. One run means one grid — see resolveSeedPurgeGrid.
	seedPurgeGrid *tms.TileMatrixSet
	// seedWriteTiers is the resolved --cache-tiers value. nil means no
	// restriction: write every tier.
	seedWriteTiers []string
)

// resolveCacheTiers turns the --cache-tiers flag into the list of tier names
// this run may write, or nil for "no restriction".
//
//	unset      the last tier in read order — the durable one by construction
//	all        every tier
//	a,b,c      exactly those, validated against the whole tree
//
// The default is the last tier rather than every tier because the alternative
// floods the hot tier with cold tiles in seed order, evicting the live working
// set — the exact harm the layered cache exists to avoid. "Durable" resisted
// definition (a file tier can carry a ttl; an s3 bucket under a lifecycle policy
// has none yet is not permanent), and position needs no inference: read order
// runs hot to durable, so the last tier *is* the durable one.
//
// It rests on that ordering, and nothing enforces it. A chain of s3 then redis
// is legal, and makes this write the hot tier and skip the durable one.
//
// A single-backend cache is unaffected: one cache is also the last cache, so
// there is nothing to restrict.
func resolveCacheTiers(c cache.Interface, flag string) ([]string, error) {
	known := multi.TierNames(c)

	flag = strings.TrimSpace(flag)

	switch {
	case len(known) == 0:
		// not a chain; there is nothing to target
		if flag != "" && !strings.EqualFold(flag, CacheTiersAll) {
			return nil, fmt.Errorf("--cache-tiers=%v: the configured cache has no tiers", flag)
		}
		return nil, nil

	case strings.EqualFold(flag, CacheTiersAll):
		return nil, nil

	case flag == "":
		last, ok := multi.LastTierName(c)
		if !ok {
			return nil, nil
		}
		log.Infof("cache seed/purge: writing tier (%v) only. pass --cache-tiers=%v to write every tier", last, CacheTiersAll)
		return []string{last}, nil
	}

	var names []string
	for _, name := range strings.Split(flag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// Validated at startup rather than silently no-oping at run time: a
		// typo in a cron job would otherwise look like a successful seed that
		// wrote nothing.
		if !slices.Contains(known, name) {
			return nil, multi.ErrUnknownTier{Name: name, Known: known}
		}
		names = append(names, name)
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("--cache-tiers was given no tier names. known tiers: %v", known)
	}

	return names, nil
}

var SeedPurgeCmd = &cobra.Command{
	Use:     "seed",
	Aliases: []string{"purge"},
	Short:   "seed or purge tiles from the cache",
	Long:    "command to seed or purge tiles from the cache",
	Example: "shigola cache seed --bounds lng,lat,lng,lat",
}

func init() {
	setupMinMaxZoomFlags(SeedPurgeCmd, 0, atlas.MaxZoom)
	SeedPurgeCmd.PersistentFlags().StringVarP(&cacheMap, "map", "", "", "map name as defined in the config")
	SeedPurgeCmd.PersistentFlags().IntVarP(&cacheConcurrency, "concurrency", "", runtime.NumCPU(), "the amount of concurrency to use. defaults to the number of CPUs on the machine")
	SeedPurgeCmd.PersistentFlags().BoolVarP(&cacheOverwrite, "overwrite", "", false, "overwrite the cache if a tile already exists (default false)")
	SeedPurgeCmd.PersistentFlags().Int64VarP(&cacheLogThreshold, "log-threshold", "", 0, "during seeding, only log tiles that take this number of milliseconds or longer to render (default all tiles)")
	SeedPurgeCmd.PersistentFlags().StringVarP(&cacheTiers, "cache-tiers", "", "", `for a layered cache (type = "multi"), the comma-separated tier names this run may write. defaults to the last tier in read order — the durable one; use "all" to write every tier, which is what you want to pre-warm. with --overwrite, the tiers NOT named here are purged after the write, so the hot tier stops serving pre-update tiles. no effect on a single-backend cache`)

	SeedPurgeCmd.PersistentFlags().StringVarP(&cacheTileMatrixSet, "tile-matrix-set", "", "", "the tiling scheme to seed or purge, by tileMatrixSetId. one run covers one scheme. defaults to the map's own scheme when --map is given, otherwise WebMercatorQuad. every targeted map must support it")

	SeedPurgeCmd.Flags().StringVarP(&cacheBounds, "bounds", "", "-180,-85.0511,180,85.0511", "lng/lat bounds to seed the cache with in the format: minx, miny, maxx, maxy")
	SeedPurgeCmd.Flags().IntVarP(&cacheBoundsSRID, "bounds-srid", "", int(proj.EPSG4326), "the srid --bounds are given in. only 4326 (lng/lat) is supported; use --tile-matrix-set to choose the tiling scheme")

	SeedPurgeCmd.PersistentPreRunE = seedPurgeCmdValidatePersistent
	SeedPurgeCmd.PreRunE = seedPurgeCmdValidate
	SeedPurgeCmd.RunE = seedPurgeCommand

	SeedPurgeCmd.SetUsageTemplate(defaultUsage)

	SeedPurgeCmd.AddCommand(TileListCmd)
	SeedPurgeCmd.AddCommand(TileNameCmd)
}

// validateTileInGrid reports whether a named tile exists in the run's scheme.
//
// The active schemes differ in width — WorldCRS84Quad has 2*2^z columns where
// WebMercatorQuad has 2^z — so a tile name valid in one can name nothing in the
// other. Left unchecked, seeding it would generate and store a tile no request
// can ask for, and the run would report success.
func validateTileInGrid(tile slippy.Tile, grid *tms.TileMatrixSet) error {
	if grid == nil {
		// The parent command resolves the run's scheme before this runs, so a
		// nil here means that ordering changed. Falling back to the default
		// would defeat the check: in a WorldCRS84Quad run it would validate
		// against WebMercatorQuad and pass exactly the tiles this exists to
		// reject.
		return fmt.Errorf("no tile matrix set has been resolved for this run")
	}

	if err := grid.ValidateTile(int(tile.Z), int64(tile.X), int64(tile.Y)); err != nil {
		var outside tms.ErrTileOutsideMatrix
		if errors.As(err, &outside) {
			return err
		}

		return fmt.Errorf("tile matrix set %v has no tile matrix %v: %w", grid.ID(), tile.Z, err)
	}

	return nil
}

// resolveSeedPurgeGrid picks the TileMatrixSet this run enumerates tiles in and
// writes them under.
//
// --tile-matrix-set wins. Without it, a run scoped to one map takes that map's
// default grid, and a run over every map takes WebMercatorQuad — what tegola
// seeded before the grid was configurable.
//
// A targeted map that does not support the chosen grid is an error, not a skip.
// A run enumerates one grid, so seeding such a map would walk indices that mean
// nothing in its grid: the run would report success having written tiles no
// request will ever ask for.
func resolveSeedPurgeGrid() (*tms.TileMatrixSet, error) {
	id := cacheTileMatrixSet
	switch {
	case id != "":
	case cacheMap != "" && len(seedPurgeMaps) == 1:
		id = seedPurgeMaps[0].TileGrid().ID()
	default:
		id = tms.WebMercatorQuad
	}

	grid, err := tms.Get(id)
	if err != nil {
		return nil, fmt.Errorf("tile matrix set %v: %w", id, err)
	}

	var unsupported []string
	for _, m := range seedPurgeMaps {
		if !m.SupportsTileGrid(grid.ID()) {
			unsupported = append(unsupported, m.Name)
		}
	}

	if len(unsupported) > 0 {
		return nil, fmt.Errorf(
			"maps %v do not support tile matrix set %v; re-run with --tile-matrix-set, or scope the run with --map",
			unsupported, grid.ID(),
		)
	}

	return grid, nil
}

// seedPurgeCmdValidate will validate the persistent flags and set associated variables as needed
func seedPurgeCmdValidatePersistent(cmd *cobra.Command, args []string) (err error) {

	if cmd.HasParent() {
		// run the parents Persistent Run commands.
		pcmd := cmd.Parent()
		if pcmd.PersistentPreRunE != nil {
			if err := pcmd.PersistentPreRunE(pcmd, args); err != nil {
				return err
			}
		}
	}

	// check if the user defined a single map to work on
	if cacheMap != "" {
		m, err := atlas.GetMap(cacheMap)
		if err != nil {
			return err
		}

		seedPurgeMaps = []atlas.Map{m}
	} else {
		seedPurgeMaps = atlas.AllMaps()
		if len(seedPurgeMaps) == 0 {
			return fmt.Errorf("expected at least one map to be defined. check your config")
		}
	}

	if seedPurgeGrid, err = resolveSeedPurgeGrid(); err != nil {
		return err
	}

	// Find the seed command and find out what it was called as.
	seedcmd := cmd
	cmdName := ""
	for seedcmd != nil {
		if seedcmd.Name() == "seed" {
			cmdName = seedcmd.CalledAs()
			break
		}
		seedcmd = seedcmd.Parent()
	}

	//cmdName := strings.ToLower(strings.TrimSpace(cmd.CalledAs()))
	switch cmdName {
	case "purge":
		seedPurgeWorker = withCacheIntent(purgeWorker)
	case "seed":
		seedPurgeWorker = withCacheIntent(seedWorker(cacheOverwrite, cacheLogThreshold))
	default:

		return fmt.Errorf("expected purge/seed got (%v) for command name", cmdName)
	}
	// After the parent's PersistentPreRunE above, so the cache is configured
	// and its tier names are resolvable.
	if seedWriteTiers, err = resolveCacheTiers(atlas.GetCache(), cacheTiers); err != nil {
		return err
	}

	build.Commands = append(build.Commands, "cache", cmdName)

	return nil

}

// validateBoundsSRID checks what --bounds-srid is allowed to say.
//
// The flag describes --bounds, which are validated as lng/lat and handed to the
// tiling scheme as geographic coordinates; it does not, and no longer appears
// to, select the scheme the run enumerates — that is --tile-matrix-set.
//
// So a projected srid describes nothing the flag can honour. It is rejected
// rather than accepted and ignored: a run whose bounds were meant as metres
// would otherwise seed a silently wrong area and report success.
func validateBoundsSRID(srid int) error {
	switch proj.EPSGCode(srid) {
	case proj.WGS84:
		return nil
	default:
		return fmt.Errorf(
			"--bounds-srid=%d: bounds are lng/lat, so only %d is supported. to seed another tiling scheme use --tile-matrix-set",
			srid, int(proj.WGS84),
		)
	}
}

func seedPurgeCmdValidate(cmd *cobra.Command, args []string) (err error) {
	if err := validateBoundsSRID(cacheBoundsSRID); err != nil {
		return err
	}

	// validate and set bounds flag
	boundsParts := strings.Split(strings.TrimSpace(cacheBounds), ",")
	if len(boundsParts) != 4 {
		return fmt.Errorf("invalid value for bounds (%v). expecting minx, miny, maxx, maxy", cacheBounds)
	}

	var ok bool

	if seedPurgeBounds[0], ok = IsValidLngString(boundsParts[0]); !ok {
		return fmt.Errorf("invalid lng value(%v) for bounds (%v)", boundsParts[0], cacheBounds)
	}
	if seedPurgeBounds[1], ok = IsValidLatString(boundsParts[1]); !ok {
		return fmt.Errorf("invalid lat value(%v) for bounds (%v)", boundsParts[1], cacheBounds)
	}
	if seedPurgeBounds[2], ok = IsValidLngString(boundsParts[2]); !ok {
		return fmt.Errorf("invalid lng value(%v) for bounds (%v)", boundsParts[2], cacheBounds)
	}
	if seedPurgeBounds[3], ok = IsValidLatString(boundsParts[3]); !ok {
		return fmt.Errorf("invalid lat value(%v) for bounds (%v)", boundsParts[3], cacheBounds)
	}

	// get the zoom ranges
	if err = minMaxZoomValidate(cmd, args); err != nil {
		return err
	}

	return nil
}

func seedPurgeCommand(_ *cobra.Command, _ []string) (err error) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer gdcmd.New().Complete()
	gdcmd.OnComplete(provider.Cleanup)
	gdcmd.OnComplete(observability.Cleanup)
	atlas.StartSubProcesses()

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-gdcmd.Cancelled():
			cancel()
		}
	}()

	log.Info("zoom list: ", zooms)
	log.Info("tile matrix set: ", seedPurgeGrid.ID())
	tileChannel := generateTilesForBounds(ctx, seedPurgeBounds, zooms, seedPurgeGrid)

	return doWork(ctx, tileChannel, seedPurgeMaps, cacheConcurrency, seedPurgeWorker)
}

// generateTilesForBounds streams every tile of grid, at each of zooms, covering
// the given lng/lat bounds.
//
// The bounds are geographic whatever --bounds-srid says — they are validated as
// lng/lat — and the grid decides the tile indices. Those were separate notions
// before only by accident: the old code built a slippy grid from the bounds
// SRID, which for the default 4326 produced WebMercator indices anyway.
func generateTilesForBounds(ctx context.Context, bounds [4]float64, zooms []uint, grid *tms.TileMatrixSet) *TileChannel {

	tce := &TileChannel{
		channel: make(chan slippy.Tile),
	}

	if grid == nil {
		grid = atlas.DefaultTileGrid()
	}

	go func() {
		defer tce.Close()

		// west/south/east/north, normalized so that a caller passing the corners
		// in either order covers the same ground rather than reading as an
		// antimeridian crossing.
		west, east := math.Min(bounds[0], bounds[2]), math.Max(bounds[0], bounds[2])
		south, north := math.Min(bounds[1], bounds[3]), math.Max(bounds[1], bounds[3])

		for _, z := range zooms {

			tiles, err := grid.Tiles(west, south, east, north, []int{int(z)}, true)
			if err != nil {
				tce.setError(fmt.Errorf("got error trying to get tiles: %w", err))
				tce.Close()
				return
			}
			for _, tile := range tiles {
				t := slippy.Tile{Z: slippy.Zoom(tile.Z), X: uint(tile.X), Y: uint(tile.Y)}
				select {
				case tce.channel <- t:
				case <-ctx.Done():
					// we have been cancelled
					return
				}
			}
		}
	}()
	return tce
}
