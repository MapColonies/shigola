# To build, run in root of shigola source tree:
#
#	$ git clone git@github.com:mapcolonies/shigola.git or git clone https://github.com/mapcolonies/shigola.git
#	$ cd shigola
#	$ docker build -t shigola .
#
# To use with local files, add file data sources (i.e. Geopackages) and config as config.toml to a
# local directory and mount that directory as a volume at /opt/shigola_config/.  Examples:
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
#
# Example gpkg use:
#  $ mkdir docker-config
#  $ cp my-config-file docker-config/config.toml
#  $ cp my-db.gpkg docker-config/
#  $ docker run -v /path/to/docker-config:/opt/shigola_config -p 8080 shigola serve

# Intermediary container for building.
#
# The Go version must track the go directive in go.mod: the module sets no
# toolchain directive, so a lower Go fails with "go.mod requires go >= ..."
# rather than downloading one. Also pinned in .devcontainer/Dockerfile and
# .github/actions/amazon-linux-build-action/Dockerfile.
FROM golang:1.26.6-alpine3.23 AS build

ARG BUILDPKG="github.com/mapcolonies/shigola/internal/build"
ARG VER="Version Not Set"
ARG BRANCH="not set"
ARG REVISION="not set"
ENV VERSION="${VER}"
ENV GIT_BRANCH="${BRANCH}"
ENV GIT_REVISION="${REVISION}"
ENV BUILD_PKG="${BUILDPKG}"

# Only needed for CGO support at time of build, results in no noticable change in binary size
# incurs approximately 1:30 extra build time (1:54 vs 0:27) to install packages.  Doesn't impact
# development as these layers are drawn from cache after the first build.
RUN apk update \
	&& apk add build-base

# Set up source for compilation
RUN mkdir -p /go/src/github.com/mapcolonies/shigola
COPY . /go/src/github.com/mapcolonies/shigola

RUN env

# Build binary
RUN cd /go/src/github.com/mapcolonies/shigola/cmd/shigola \
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