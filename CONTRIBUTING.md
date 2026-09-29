# Contributing to the TMDB Metadata Plugin

The [Prairie contribution guide](https://github.com/prairie-server/prairie-server/blob/main/CONTRIBUTING.md)
covers project-wide coordination, focused changes, evidence, AI disclosure, and
pull request expectations. Those requirements apply here; this guide adds the
plugin-specific workflow.

## Before you start

Open an [issue](https://github.com/prairie-server/prairie-plugin-metadata-tmdb/issues)
before changing matching, metadata mapping, image resolution, configuration, or
the advertised capabilities. This repository owns TMDB provider behavior;
plugin contracts belong in
[`prairie-plugin-sdk`](https://github.com/prairie-server/prairie-plugin-sdk), while host
metadata orchestration belongs in
[`prairie-server`](https://github.com/prairie-server/prairie-server).

## Development setup

Use the Go version declared in `go.mod`. A local `go.work` may point at a sibling
SDK checkout while developing both repositories, but committed code and CI must
resolve the dependencies pinned in `go.mod` with `GOWORK=off`. Never commit provider
credentials issued to you, captured private data, or a local filesystem
`replace` directive.

## Validate your change

```sh
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go build ./...
GOWORK=off go run . manifest >/dev/null
gofmt -l .
GOWORK=off golangci-lint run ./...
GOWORK=off go test ./... -count=1 -covermode=atomic -coverprofile=coverage.out
./scripts/check-coverage.sh coverage.out
```

The manifest command must exit successfully. `gofmt -l .` should print nothing;
if it reports unrelated pre-existing drift, none of the Go files touched by your
change may appear in the output. Do not add to the output, and report what
remains. Add focused coverage for matching, field mapping, image paths,
pagination, and upstream error handling when those behaviors change.
CI runs golangci-lint v2.14.0 and enforces a 95% statement coverage floor
(`scripts/check-coverage.sh`); the lint and coverage commands above reproduce
those checks locally.
Locally, `golangci-lint run` checks the whole repository, while CI reports only
issues new in the pull request (`only-new-issues`), so the local run is the
stricter of the two.

## Open the pull request

Use a Conventional Commit title, explain any matching, attribution, or upstream
service risk, and paste the actual validation results. Read the
[AI-assisted contribution policy](https://github.com/prairie-server/prairie-server/blob/main/docs/ai-contributions.md)
and include its disclosure block.
