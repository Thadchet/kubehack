# Kubehack

A desktop app for looking after your Kubernetes clusters on a Mac. See what's
running, read logs, open a shell in a container, and manage Helm releases from
one window, without typing `kubectl` commands.

Kubehack uses the same access you already have with `kubectl`. There's nothing
to install in your cluster and no account to create, and it only talks to your
own clusters and any chart source you choose.

## What you can do

- Browse every kind of resource, including custom ones, with live updates
- Read and search pod logs
- Open a terminal inside a container
- Edit YAML and review a diff before applying it
- Scale, restart and delete, with a confirmation first
- Forward a port from a pod or service to your Mac
- See CPU and memory usage
- Install, upgrade, roll back and uninstall Helm charts

## Before you start

You need a Mac and access to a Kubernetes cluster. If `kubectl get pods` works
in your terminal, Kubehack will work too. It has been tested on macOS 26 with
Apple silicon.

There's no download yet, so you build the app on your Mac once. This takes a few
minutes the first time.

## Install

**1. Install the tools.** Open Terminal and run these. The second line needs
[Homebrew](https://brew.sh).

```sh
xcode-select --install
brew install go node
```

If the first command says the tools are already installed, carry on.

**2. Download Kubehack and start it.**

```sh
git clone https://github.com/Thadchet/kubehack.git
cd kubehack
./run.sh
```

The app opens when the build finishes. To open it again later, go back to the
`kubehack` folder and run `./run.sh`, or open `build/bin/kubehack.app` from
Finder. You can also drag that file to your Applications folder.

To update to the latest version:

```sh
git pull
./run.sh
```

Quit Kubehack first, or `./run.sh` will bring back the old version that's still
open.

## Your first five minutes

**Choose a cluster.** Use the menu at the top left. Each cluster gets its own
color, so you can see which one you're in.

**Protect anything important.** Kubehack can change and delete real resources.
Tick **Read-only** at the top right for any cluster you only want to look at,
such as production. This blocks every change, shell and new port forward, and
Kubehack remembers it for that cluster.

**Find something.** Pick a resource type on the left, such as Pods. Choose a
namespace at the top, or press `/` to filter by name. Click a row to see its
details and events.

**Work with it.** The buttons in the details panel depend on what you selected:

| Button | What it does |
| --- | --- |
| View logs | Follow a pod's logs in the panel at the bottom |
| Open shell | Open a terminal in the container |
| Forward port | Reach a pod or service from your Mac, for example in your browser |
| Edit | Change the YAML, then review the changes before applying them |
| Scale | Change the number of replicas |
| Restart | Replace the pods, like `kubectl rollout restart` |
| Delete | Delete the object, after you type its name to confirm |

**Use Helm.** Choose **Releases** under Helm on the left. Select **Install a
chart** and enter a chart, such as `oci://ghcr.io/stefanprodan/charts/podinfo`
or `bitnami/nginx` if you've added that repository with `helm repo add`. Select
**Preview** to see what will be created before you confirm. Select a release to
upgrade it, roll it back to an earlier revision, or uninstall it.

### Keyboard shortcuts

| Key | Action |
| --- | --- |
| `j` `k` or arrow keys | Move through the table |
| `/` | Filter by name |
| `Esc` | Clear the filter or close the panel |
| `l` | View logs |
| `s` | Open a shell |
| `f` | Forward a port |
| `e` | Edit |

## Good to know

- Closing a terminal tab ends everything running in that shell, like closing a
  terminal window. Use `nohup` for anything that needs to carry on.
- Port forwards stop when you quit Kubehack. If one drops, a message at the top
  says why and offers **Reconnect**.
- Any program on your Mac can reach a forwarded port, as with
  `kubectl port-forward`. Other computers can't.
- Upgrading a Helm release replaces all its values with what's in the editor. If
  you delete a line, that value goes back to the chart's default.
- Helm values appear on screen when you upgrade, so passwords in them are visible.

## If something goes wrong

**"No clusters found in kubeconfig."** Kubehack can't find a cluster. Run
`kubectl config get-contexts` in Terminal. If that's empty too, add your cluster
with your provider's tool, for example `gcloud container clusters get-credentials`
or `aws eks update-kubeconfig`, then reopen Kubehack.

**"Could not reach" a cluster.** Check that `kubectl get pods` works in Terminal,
and that any VPN you need is connected. Your sign-in may have expired, so sign in
again with your provider's tool. Then select **Try again**.

**CPU and memory aren't shown.** The cluster needs
[metrics-server](https://github.com/kubernetes-sigs/metrics-server). Everything
else works without it.

**"Could not open a shell."** The container has no shell, which is common in
minimal images.

**A port is already in use.** Leave **Local port** empty, and Kubehack picks a
free one.

**`./run.sh` says `go` or `npm` is missing.** Finish the first install step, then
open a new Terminal window.

**macOS won't open a copy from another Mac.** The app isn't signed, so each Mac
needs to build its own.

## Remove Kubehack

Delete the `kubehack` folder, any copy in Applications, and the saved settings:

```sh
rm -rf ~/Library/Application\ Support/kubehack
```

Kubehack doesn't install anything in your clusters. Helm releases you installed
stay there until you uninstall them.

## For developers

Kubehack uses Wails, Go and React.

**Run with hot reload.** This starts the API and the UI in your browser. Press
Ctrl+C to stop both. `wails dev` doesn't work, because the API only accepts the
built app's origin. Read-only choices aren't saved in this mode.

```sh
./run.sh dev
```

Set `KUBEHACK_DEBUG=1` to log API requests. Set `KUBECONFIG` before either
`run.sh` command to limit a run to certain clusters.

**Test.**

```sh
go test -race ./internal/...
cd frontend && npx tsc --noEmit
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
