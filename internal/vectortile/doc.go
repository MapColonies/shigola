// Package vectortile is the Mapbox Vector Tile wire format, generated from the
// specification's own schema.
//
// It exists because the only other Go binding for that schema --
// go-spatial/geom's encoding/mvt/vector_tile -- is pre-APIv2 protoc-gen-go
// output that imports the deprecated github.com/golang/protobuf, and geom has
// only ever released v0.1.0, so there is no version of it to upgrade to
// (MAPCO-11516). Generating the schema here is what let that dependency leave
// the module rather than persist as something nothing in this tree asked for.
//
// Nothing here is hand-written. vector_tile.pb.go is regenerated from
// vector_tile.proto; see the go:generate line below for how, and change the
// schema rather than the output.
//
// One hazard worth knowing about: the messages register under the proto package
// name the specification gives them, vector_tile, in the process-wide protobuf
// registry. Linking geom's encoding/mvt back into any binary or test would put
// a second claim on those names and panic at init. That is loud rather than
// subtle, and the module no longer depends on geom's mvt packages at all, so
// nothing is guarding against it beyond this note.
package vectortile

// Regenerating needs protoc and a protoc-gen-go matching the
// google.golang.org/protobuf version in go.mod. The devcontainer image carries
// both. The schema is frozen -- vector-tile-spec 2.1, unchanged since 2016 --
// so this should not need running.
//
//go:generate protoc --go_out=. --go_opt=paths=source_relative vector_tile.proto
