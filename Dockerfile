# To build, run in root of shigola source tree:
#
#	$ git clone git@github.com:mapcolonies/shigola.git or git clone https://github.com/mapcolonies/shigola.git
#	$ cd shigola
#	$ docker build -t shigola .
#
# To use a local config, put it in a local directory as config.toml and mount that directory as a
# volume at /opt/shigola_config/.  Examples:
#
# To display command-line options available:
#  
#	$ docker run --rm shigola
#
# Example PostGIS use w/ http-based config:
#
#	$ docker run -p 8080 shigola --config http://my-domain.com/config serve
#
# Example PostGIS use w/ local config:
#	$ mkdir docker-config
#	$ cp my-config-file docker-config/config.toml
#	$ docker run -v /path/to/docker-config:/opt/shigola_config -p 8080 shigola serve

# Intermediary container for building.
#
# The Go version must track the go directive in go.mod: the module sets no
# toolchain directive, so a lower Go fails with "go.mod requires go >= ..."
# rather than downloading one. Also pinned in .devcontainer/Dockerfile.
FROM golang:1.26.6-alpine3.23 AS build

# Must match go.mod's module path exactly, case included: the linker silently
# ignores an -X it cannot resolve, so a wrong case builds fine and reports
# "version not set" (MAPCO-11500). Pinned by TestStampPathMatchesModulePath.
ARG BUILDPKG="github.com/MapColonies/shigola/internal/build"
ARG VER="Version Not Set"
ARG BRANCH="not set"
ARG REVISION="not set"
ENV VERSION="${VER}"
ENV GIT_BRANCH="${BRANCH}"
ENV GIT_REVISION="${REVISION}"
ENV BUILD_PKG="${BUILDPKG}"

# Explicit rather than inherited: with no cgo-dependent package left
# (MAPCO-11489), leaving this to the default would decide the binary's linkage
# by whether the builder happens to have a C compiler. Pinning it to 0 makes the
# image's binary statically linked and reproducible, and removes the build-base
# install that used to precede this build for roughly 1:30.
ENV CGO_ENABLED=0

WORKDIR /go/src/github.com/MapColonies/shigola

# Dependencies before source, in a layer of their own, so that a source-only
# change reuses the downloaded module cache instead of fetching it again. This
# step is the build's one network dependency: nothing is vendored, and go.sum
# is what makes the download trustworthy (MAPCO-11521).
COPY go.mod go.sum ./
RUN go mod download

# Set up source for compilation
COPY . .

RUN env

# Build binary
RUN cd cmd/shigola \
	&& go build -v  \
	-ldflags "-w -X '${BUILD_PKG}.Version=${VERSION}' -X '${BUILD_PKG}.GitRevision=${GIT_REVISION}' -X '${BUILD_PKG}.GitBranch=${GIT_BRANCH}'" \
	-gcflags "-N -l" \
	-o /opt/shigola \
	&& chmod a+x /opt/shigola

# Create minimal deployment image, just alpine & the binary
FROM alpine:3.18

RUN apk update \
	&& apk add ca-certificates \
	&& rm -rf /var/cache/apk/*

COPY --from=build /opt/shigola /opt/
WORKDIR /opt
ENTRYPOINT ["/opt/shigola"]
