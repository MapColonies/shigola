# Amazon Linux is used to build shigola_linux so the CGO requirements are linked correctly
FROM amazonlinux:latest

# install build deps
RUN yum install -y tar gzip gcc

# install Go
# Must track the go directive in go.mod: the module sets no toolchain
# directive, so a lower Go here fails with "go.mod requires go >= ..."
# rather than downloading one. The checksum is from https://go.dev/dl.
ENV GOLANG_VERSION=1.26.6
ENV GOLANG_VERSION_SHA256=708effb774be8237570d0add163225abbdfaf4fca28b2611df167beba4feef89

RUN curl -o golang.tar.gz https://dl.google.com/go/go$GOLANG_VERSION.linux-amd64.tar.gz \
	&& echo "$GOLANG_VERSION_SHA256 golang.tar.gz" | sha256sum --strict --check \
	&& tar -C /usr/local -xzf golang.tar.gz \
	&& rm golang.tar.gz

ENV PATH=/usr/local/go/bin:$PATH

# entrypoint.sh holds the build instructions for shigola_lambda
COPY entrypoint.sh /entrypoint.sh

# run the build script when this container starts up
ENTRYPOINT ["/entrypoint.sh"]
