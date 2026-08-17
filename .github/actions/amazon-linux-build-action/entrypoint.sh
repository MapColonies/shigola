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
GOARCH=${GOARCH} go build \
	-mod vendor \
	-tags lambda.norpc \
	-ldflags "-w -X ${BuildPkg}.Version=${VERSION} -X ${BuildPkg}.GitRevision=${GIT_REVISION} -X ${BuildPkg}.GitBranch=${GIT_BRANCH}" \
	-o bootstrap
