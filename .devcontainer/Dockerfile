# Must track the go directive in go.mod: the module sets no toolchain
# directive, so a lower Go fails with "go.mod requires go >= ..." rather
# than downloading one. Also pinned in Dockerfile.
FROM golang:1.26.6-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        build-essential \
        ca-certificates \
        curl \
        git \
        less \
        make \
        postgresql-client \
        protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

# protoc and its Go plugin, for `cd internal/vectortile && go generate`. Pinned
# to the google.golang.org/protobuf version in go.mod rather than @latest:
# generated code asserts at compile time that its gencode API version is one the
# runtime still supports, so a plugin that has moved on from the vendored runtime
# emits a file that will not build. Bump the two together.
RUN GOBIN=/usr/local/bin go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12

WORKDIR /workspace/shigola

CMD ["sleep", "infinity"]
