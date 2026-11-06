# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Kubehack is a Kubernetes desktop app built with Wails, Go and React. It also runs in a browser from a Docker image. `README.md` holds the user guide and its limits.

## Commands

```sh
./run.sh                # build if the code changed, then open the Mac app
./run.sh dev            # API and Vite with hot reload, in a browser (`wails dev` doesn't work)
./run.sh docker         # build the image from source and run it; KUBEHACK_PORT changes the port
./run.sh release        # build both zips into releases/, which are committed

go test -race ./internal/...                     # all Go tests
go test ./internal/api -run TestGuard            # one Go test
cd frontend && npx tsc --noEmit                  # type check
cd frontend && npm test                          # all frontend tests (vitest)
cd frontend && npx vitest run src/features/editor/dataValues.test.ts   # one file

kind create cluster --name kubehack              # switches the current kubectl context
go test -tags integration ./internal/api         # needs kind, kubectl, helm and network
```

Integration tests create and delete objects, and always name the `kind-kubehack` context. Restore your kubectl context after creating the cluster.

`KUBEHACK_DEBUG=1` logs API requests. Setting `KUBECONFIG` limits a run to certain clusters.

## Architecture

The UI talks to a local HTTP and WebSocket API in `internal/api`, not to Wails bindings. The only binding is `App.Connection()`, which hands the UI the API's address and token. New features belong in the API, so that they work in all three ways of running:

| Mode | Entry point | How the UI gets the address and token |
| --- | --- | --- |
| Desktop | `main.go`, `app.go` | Wails binding. Random port and token for each launch |
| Dev | `cmd/kubehack-dev` | `VITE_KUBEHACK_API` and `VITE_KUBEHACK_TOKEN`, set by `run.sh` |
| Docker | `cmd/kubehack-docker` | Same origin, with the token in the URL fragment, then session storage |

`frontend/src/api.ts` chooses between these. Features live in `frontend/src/features/<name>`.

The Docker server also serves the UI from the API's port, listens on `0.0.0.0`, and refuses port forwards. At startup it writes a copy of the kubeconfig for the container (`cluster.WriteContainerKubeconfig`) and points `KUBECONFIG` at it, which is how Helm and every other client pick it up. That's also why new clusters need a restart there.

Go packages: `cluster` reads kubeconfig and the login shell's environment, `resources` handles discovery, watching, apply and actions, `stream` handles logs, shells and port forwards, `helm` wraps the Helm SDK, and `settings` saves preferences.

## Rules that span files

- **Request guard.** `internal/api/guard.go` checks `Host`, `Origin` and the token, because any web page can reach a loopback port. The `Host` check compares the port the server listens on, so in Docker the host and container ports must match.
- **Read-only mode is enforced in the API**, not just by hiding buttons. Every new write, shell or forward route must call `rejectReadOnly` or `writeIfReadOnly`.
- **Watched objects are redacted** (`internal/resources/sanitize.go`): Secret values are blank and last-applied is removed. Any write path must fetch the real object through `/object` first, or it would wipe Secret values.
- **Clients are built per context** with `cluster.RESTConfig(name)`. The app never changes current-context, because that would affect the user's kubectl.
- **The build-time Wails bindings are committed** in `frontend/wailsjs`. The Docker build uses them without running Wails.

## CI and releases

`.github/workflows/ci.yml` runs the tests on every pull request and push, and publishes only if they pass. Pushing to `main` publishes `ghcr.io/thadchet/kubehack:edge` and `:edge-gke`. Pushing a tag such as `v1.0.0` publishes `:1.0.0` and `:1.0.0-gke`, and moves `:latest` and `:gke`, which are what the README tells people to run. The `Dockerfile` has one target for each flavor: `kubehack`, and `kubehack-gke`, which adds `gcloud`.

`go.mod` sets the Go patch version on purpose, because `./run.sh release` builds the desktop zips with it. Raise it when `govulncheck ./...` reports the standard library, then rebuild the zips.
