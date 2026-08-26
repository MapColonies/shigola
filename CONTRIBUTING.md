# Welcome

> **Shigola is a fork of [Tegola](https://github.com/go-spatial/tegola).** This file is upstream's
> contribution guide, kept because its conventions still govern this codebase — the `gofmt -s` rule,
> the error-variable form, and running the tests with CGO both enabled and disabled all still apply.
>
> **Where to send a change:**
>
> - A fix or feature that is **generally useful** belongs **upstream**, at
>   [go-spatial/tegola](https://github.com/go-spatial/tegola). Follow this guide as written: base the
>   PR on the release-candidate branch named for the next version, not `master`.
> - A change to what **Shigola adds** — OGC API - Tiles, tile matrix sets, the layered cache — or to
>   this fork's build and branding, belongs here, at
>   [MapColonies/shigola](https://github.com/MapColonies/shigola).
>
> Links below point at upstream's issue tracker and Slack, and are correct for upstream
> contributions. For Shigola-specific work, use this repository's issues instead. The build commands
> below refer to `cmd/shigola` in this fork.

---

Thank you for even thinking about contributing! We are excited to have you. This document is intended as a guide to help your through the contribution process. This guide assumes a you have a basic understanding of Git and Go.

For sensitive security-related issue please start a conversation with a core contributor on the [#go-spatial](https://invite.slack.golangbridge.org/) channel in the [gophers slack](https://invite.slack.golangbridge.org/) organization.

This project and everyone who is participating in it is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).
By participating, you are expected to uphold this code. Please report unacceptable behavior to the [#go-spatial](https://invite.slack.golangbridge.org/) channel in the [gophers slack](https://invite.slack.golangbridge.org/) organization or to one of the Core Contributors.

## There are several places where you can contribute. 

### Found a bug or something does not feel right?

Everything we do is done through [issues](https://github.com/go-spatial/tegola/issues). The first thing to do is to search the current issues to see if it is something that has been reported or requested already. If you are unable to find an issue that is similar or are unsure just file a new issue. If you find one that is similar, you can add a comment to add additional details, or if you have nothing new to add you can “+1” the issue.

* If you are unable to find an issue that is similar or are unsure go ahead and file a new one. 
* If it is a bug your can use the following [template](https://github.com/go-spatial/tegola/issues/new?template=bug.md). 
* If this is a rendering bug, please include the relevant data set and configuration file. 
* If it is a feature request use the following [template](https://github.com/go-spatial/tegola/issues/new?template=feature.md).
* If this is a feature request, please include a description of what the feature is, and the use case for the feature.

Once you have filed an issue, we will discuss it in the issue. If we need more information or you have further questions about that issue, this is the place to ask. This is the place where we will discuss the design of the fix or feature. Any pull request that adds a feature or fixes an issue should reference the issue number.

If you have changes to for the Tegola.io website — documentation on the website, tutorials, translation or anything else — the process is similar but on a different [repository](https://github.com/go-spatial/tegola-docs) (https://github.com/go-spatial/tegola-docs).

Don’t be afraid to reach out if you have any questions.  You can reach us on the gophers Slack on the channel #tegola or #go-spatial. You can get an invite into the gophers Slack via (https://invite.slack.golangbridge.org/)

## Making a Contribution to the code base.

For the Tegola project our master branch is always the most recent stable version of the code base. The current release candidate will be in a branch name for the next version of the software. For example if the current release is v0.6.1 the next release will be v0.7.0, the release candidate branch will be called “v0.7.0”. Please, base all of your pull requests on the release candidate branch.

### Discuss your design

All contributions are welcome, but please let everyone know what you are working on. The way to do this is to first file an issue (or claim an existing issue). In this issue, please, discuss what your plan is to fix or add the feature. Also, all design discussions should happen on the issue. If design discussions happen in a channel, reconcile the decisions to the relevant issue(s). Once, your contribution is ready, create a pull request referencing the issue. Once, a pull request is created one or more of the Core Contributors will review the pull request and may request changes. Once the changes are approved, it will be merged into the current release candidate branch.

Be sure to keep the pull request updated as merge conflicts may occur as other things get merged into the release branch before yours.

Please, note that we may push your pull request to the next release candidate at which point you will have to resolve any conflicts that occur.

### Not sure where to contribute?

Want to contribute but not sure where? Not a problem, the best thing to do is look through the issues and find one that interests you. If the issue has the label `good first issue`, it means that one of the core contributors thinks this is a good issue to start with. But, this doesn’t mean that you have to start with these issues. Go through the issues and see if someone is already working on it. If no one is, state that you will be working on the issue to claim it. If you are unsure where to start on the issue, ask in the issue and one of the Core Contributors will help you out.

## How to build from source

For Shigola, get a binary from the [releases page](https://github.com/MapColonies/shigola/releases), or use `go get -u github.com/MapColonies/shigola/cmd/shigola`. Upstream Tegola's binaries are on [its own releases page](https://github.com/go-spatial/tegola/releases).

If however you want to build the latest release candidate you will have to build from source. The first thing to do is to clone the repo (`MapColonies/shigola` for this fork, `go-spatial/tegola` for upstream) to your `GOPATH`. The simplest way to do this is to use `go get -u github.com/MapColonies/shigola`, navigate to the repository root then: 

* Checkout the current release candidate branch, (i.e. v0.15.0)
	
    (`git checkout v0.15.0`)
	
* Create a new feature branch. 
	
    (`git checkout -b issue-XXX-new_feature`)
	
* Work on the fix, and run all the tests. We need to run tests with CGO enabled and disabled.

  (`go generate ./...`) # to regenerate any autogenerated assets
  (`go test ./…`)
    (`CGO_ENABLED=0 go test ./…`)

* Make sure tegola can be built and run:

    (`cd cmd/shigola`)
    (`go build && ./tegola serve --config=path/to/config.toml`)
	
* Commit your changes (`git commit -am ‘Add some feature #XXX\n\nExtened description.'`)

### Contribute upstream:

* On github, fork the repo to into your account.
* Add a new remote pointing to your fork. 

	(`git remote add fork  git@github.com:yourname/rep.git`)
	
* Push to the branch 
	
	(`git push fork issue-XXX-new_feature`)
	
* Create a new Pull Request on GitHub against the Release Candidate branch.

For more information about this work flow, please refer to this [great explanation by Katrina Owen](https://splice.com/blog/contributing-open-source-git-repositories-go/).

## Conventions

* All code should be formatted using:
	
	(`gofmt -s ./…`).

	- if you find that running `gofmt` produces changes across parts of the code base you're not working on, submit the formatting change in a separate Pull Request. This helps decouple engineering changes from formatting changes and focused the code review efforts. 
	
* When declaring errors variables they should take the form of:
	
	(`var ErrErrorName  = errors.New("provider: canceled")`)
	
* The text should be all lowercase, and no punctuation at the end.

## Testing

For tests we use go 1.7 sub tests. Please, look at the [cmp_test.go](https://github.com/go-spatial/tegola/blob/master/geom/cmp/cmp_test.go).

### Coverage floor

CI fails a build whose total statement coverage falls below the floor recorded in
[`ci/coverage-baseline.txt`](ci/coverage-baseline.txt). The floor is **43.00%**, set from a measured
44.26% — it is a floor, not a target, and it exists to stop a silent slide rather than to describe
where coverage ought to be. Raising it is a deliberate decision to make once the coverage is really
there.

To run the same check locally you need the fixture services up, because the baseline was measured
with the PostGIS and Redis suites enabled:

```bash
docker compose up -d

RUN_POSTGIS_TESTS=yes RUN_REDIS_TESTS=yes \
  PGURI="postgres://postgres:postgres@localhost:5432/shigola?sslmode=disable" \
  PGURI_NO_ACCESS="postgres://shigola_no_access:postgres@localhost:5432/shigola?sslmode=disable" \
  PGSSLMODE=disable \
  go test -mod vendor -race -covermode atomic -coverprofile=profile.cov ./...

go run -mod vendor ./ci/coverage        # check the profile against the floor
```

`-race` alongside `-covermode atomic` is not redundant: atomic is race-safe *counting*, not race
*detection*, and the cache write path runs a goroutine pool, detached contexts and a concurrent tier
fan-out that only `-race` inspects.

Running with fewer gates enabled measures less, so the check may fail locally on a tree that is fine
in CI. Compare the per-package rows in the baseline rather than only the total.

The baseline deliberately records **only** the gates that this repository can provision for itself.
`RUN_S3_TESTS` and `RUN_HANA_TESTS` are enabled in CI but excluded from the baseline: no AWS
credentials are configured in the workflow, and the HANA connection string points at a third-party
instance the project does not control. A baseline nobody can reproduce is not a baseline.

To regenerate after a change that is *meant* to move the numbers:

```bash
go run -mod vendor ./ci/coverage -write -gates "RUN_POSTGIS_TESTS RUN_REDIS_TESTS"
```

Regenerating keeps the recorded floor unless you pass `-floor` — lowering it is meant to be an
explicit edit you justify in the pull request, not a side effect of running `-write` on a machine
with fewer services running.

