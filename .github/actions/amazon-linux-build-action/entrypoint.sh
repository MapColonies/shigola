#!/bin/sh -l

# This file expects to run in a Docker container running on GitHub Actions. 
# GitHub will automatically mount the source code directory as a Docker volume 
# using the following docker run flag:
#
#	-v "/home/runner/work/shigola/shigola":"/github/workspace"
#
# The workdir is set using the following docker run flag:
#
#	--workdir /github/workspace
#
# The VERSION env var is set using the following docker run flag:
# 
#	-e VERSION
#

# move to the shigola_lambda folder
cd cmd/shigola_lambda

# build the binary
#
# CGO_ENABLED=0 explicitly: nothing in the tree depends on cgo (MAPCO-11489), so
# a static binary is both what Lambda wants and what makes the amd64 and arm64
# artifacts identical in linkage -- the arm64 build already had cgo off, because
# cross-compiling disables it, and only the amd64 one did not.
CGO_ENABLED=0 GOARCH=${GOARCH} go build \
	-mod vendor \
	-tags lambda.norpc \
	-ldflags "-w -X ${BuildPkg}.Version=${VERSION} -X ${BuildPkg}.GitRevision=${GIT_REVISION} -X ${BuildPkg}.GitBranch=${GIT_BRANCH}" \
	-o bootstrap
