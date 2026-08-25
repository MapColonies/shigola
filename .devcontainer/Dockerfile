FROM golang:1.26.6-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        build-essential \
        ca-certificates \
        curl \
        git \
        less \
        libsqlite3-dev \
        make \
        postgresql-client \
        sqlite3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace/shigola

CMD ["sleep", "infinity"]
