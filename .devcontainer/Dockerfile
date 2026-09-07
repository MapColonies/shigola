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
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace/shigola

CMD ["sleep", "infinity"]
