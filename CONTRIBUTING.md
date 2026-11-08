# Contributing

Kubehack uses Wails, Go and React. Building needs a Mac with Go 1.26, Node 20.19
and Xcode Command Line Tools (`brew install go node`, `xcode-select --install`).

**Build and open the app.** This installs the Wails CLI if needed, and only
rebuilds if the code changed. If Go downloads fail on an unreliable connection,
run it again, or set `GOPROXY=https://goproxy.cn,direct` or `GOPROXY=direct`.

```sh
git clone https://github.com/Thadchet/kubehack.git
cd kubehack
./run.sh
```

**Make the downloads.** This builds both zips into `releases/` on a Mac. CI does
this for each release, so you only need it to try a build yourself.

```sh
./run.sh release
```

**Run with hot reload.** This starts the API and the UI in your browser. Press
Ctrl+C to stop both. `wails dev` doesn't work, because the API only accepts the
built app's origin. Read-only choices aren't saved in this mode.

```sh
./run.sh dev
```

**Run in Docker from source.** This builds the image and starts it, and opens your
browser. The UI and the API share one port. It needs Docker, not Go or Node, and
shares its **Read-only** choices with the desktop app. Set `KUBEHACK_PORT` if port
8765 is taken.

```sh
./run.sh docker
```

Set `KUBEHACK_DEBUG=1` to log API requests. Set `KUBECONFIG` before any of these
`run.sh` commands to limit a run to certain clusters.

**Release.** Tests run on every pull request and push. Pushing to `main` then
publishes the `edge` and `edge-gke` images, and builds the downloads. Pushing a
version tag, such as `v1.0.0`, also publishes that version, moves `latest` and `gke`
to it, and creates a GitHub release with the downloads. The Mac app is signed and
notarized if the repository has the `APPLE_CERTIFICATE`,
`APPLE_CERTIFICATE_PASSWORD`, `APPLE_ID`, `APPLE_TEAM_ID` and `APPLE_APP_PASSWORD`
secrets, where the certificate is a base64 Developer ID Application `.p12` file.

After a release, update `version` and `sha256` in
[Thadchet/homebrew-tap](https://github.com/Thadchet/homebrew-tap/blob/main/Casks/kubehack.rb),
then check it with `brew audit --cask --online thadchet/tap/kubehack`.

**Test.**

```sh
go test -race ./internal/...
cd frontend && npx tsc --noEmit && npm test
```

Integration tests create and delete objects, so they need a disposable cluster.
They also need `kind`, `kubectl`, `helm` and network access. Every command names
the `kind-kubehack` context. Creating the cluster switches your current kubectl
context to it, so switch back afterwards.

```sh
kind create cluster --name kubehack
go test -tags integration ./internal/api
```

**Layout.**

```text
main.go, app.go    Desktop app and its lifecycle
cmd/kubehack-dev   The API without the desktop app
cmd/kubehack-docker  The API and the UI for the Docker image
internal/api       Local HTTP and WebSocket API
internal/cluster   Kubeconfig and shell environment
internal/resources Discovery, watching, apply and actions
internal/stream    Logs, shells and port forwards
internal/metrics   metrics-server
internal/helm      Helm SDK
internal/settings  Saved preferences
frontend/src       React UI
```

The API listens on `127.0.0.1` with a random port and token for each launch, and
checks `Host` and `Origin`, because any web page can reach a loopback port. In
Docker the API listens on all of the container's interfaces, so the port must only
be published on the host's `127.0.0.1`.

## Reporting bugs

Use [GitHub Issues](https://github.com/Thadchet/kubehack/issues). For security problems, follow [SECURITY.md](SECURITY.md).
