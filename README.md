# Kubehack

A personal Kubernetes desktop manager for macOS, built with Wails (Go and
React). It reads your kubeconfig and talks to clusters directly, with no agent
to install.

- Browse any resource, including custom resources, with live updates
- Read pod logs and open a shell in a container
- Edit YAML with a diff before applying, plus scale, restart and delete
- Forward ports to pods and services
- See CPU and memory usage, if the cluster has metrics-server
- Manage Helm releases

## Requirements

- macOS
- Go 1.26 and Node 20
- Xcode Command Line Tools: `xcode-select --install`

## Run the app

```sh
./run.sh
```

This installs the Wails CLI if it's missing, builds the app if the code changed,
and opens it.

The app is unsigned, so it
opens on the machine that built it, and macOS will block a copy on another Mac.

Kubehack uses your default kubeconfig, or `KUBECONFIG` if your shell sets it.
Your shell's `PATH` is imported at startup, so credential plugins such as
`gke-gcloud-auth-plugin` work when you open the app from Finder.

To limit a session to certain clusters, point it at another kubeconfig:

```sh
KUBECONFIG=/path/to/kubeconfig ./run.sh
```

### Protect production clusters

Kubehack can change and delete real resources. Tick **Read-only** in the top bar
for any cluster you only want to look at. This is enforced by the app's server
and blocks every write, shell and new port forward. It's saved per cluster in
`~/Library/Application Support/kubehack/settings.json`.

## Keyboard shortcuts

| Key | Action |
| --- | --- |
| `j` `k` or arrow keys | Move through the table |
| `/` | Filter by name |
| `Esc` | Clear the filter or close the panel |
| `l` | View logs of the selected pod |
| `s` | Open a shell in the selected pod |
| `f` | Forward a port to the selected pod or service |
| `e` | Edit the selected object |

## Develop

The UI runs in a normal browser against the API, which gives hot reload.
`wails dev` doesn't work, because the API only accepts the built app's origin.

```sh
./run.sh dev
```

This starts the API and the UI, and opens <http://localhost:5173>. Press Ctrl+C
to stop both.

Read-only choices are kept in memory in this mode, so they don't protect a
cluster after a restart. Set `KUBEHACK_DEBUG=1` to log API requests.

## Test

```sh
go test -race ./internal/...
cd frontend && npx tsc --noEmit
```

Integration tests need a disposable cluster, because they create and delete
objects. They also need `kind`, `kubectl`, `helm` and network access to pull a
public chart. Every command names the `kind-kubehack` context.

```sh
kind create cluster --name kubehack
go test -tags integration ./internal/api
```

`kind create cluster` switches your current kubectl context to the new cluster,
so switch it back afterwards.

## Layout

```text
main.go, app.go    Desktop app and its lifecycle
cmd/kubehack-dev   The API without the desktop app
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
checks `Host` and `Origin`, because any web page can reach a loopback port.
