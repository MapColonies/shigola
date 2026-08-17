set -x

# read the hash of the geom repo
GEOM_HASH=`git -C $GOPATH/src/github.com/go-spatial/geom rev-parse --short HEAD`

SHIGOLA_HASH=`git rev-parse --short HEAD`

SHIGOLA_BRANCH=`git rev-parse --abbrev-ref HEAD`

VERSION_TAG="nonrelease_branch_${SHIGOLA_BRANCH}_hash_${SHIGOLA_HASH}_geom_${GEOM_HASH}"

LDFLAGS_VERSION="-X github.com/MapColonies/shigola/internal/build.Version=${VERSION_TAG}"
LDFLAGS_BRANCH="-X github.com/MapColonies/shigola/internal/build.GitBranch=${SHIGOLA_BRANCH}"
LDFLAGS_REVISION="-X github.com/MapColonies/shigola/internal/build.GitRevision=${SHIGOLA_HASH}"

LDFLAGS="-w ${LDFLAGS_VERSION} ${LDFLAGS_BRANCH} ${LDFLAGS_REVISION}"

go build -ldflags "${LDFLAGS}" -o "shigola_${SHIGOLA_BRANCH}" github.com/MapColonies/shigola/cmd/shigola
