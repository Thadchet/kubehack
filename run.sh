#!/usr/bin/env bash
#
# ./run.sh        Build the app if the code changed, then open it.
# ./run.sh dev    Run the API and the UI with hot reload, in a browser.
# ./run.sh release  Build the macOS and Windows downloads into releases/.
# ./run.sh docker   Run in Docker, and open it in a browser.
#
# To limit a run to certain clusters, set KUBECONFIG first.
set -euo pipefail
cd "$(dirname "$0")"

wails_version=v2.17.1
app=build/bin/kubehack.app
binary=$app/Contents/MacOS/kubehack
api_port=8765
ui_port=5173
docker_port=${KUBEHACK_PORT:-8765}

fail() {
  echo "$1" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null || fail "$1 is missing. $2"
}

port_is_free() {
  lsof -nP -iTCP:"$1" -sTCP:LISTEN >/dev/null && fail "Port $1 is in use. Stop what's using it, then try again."
  return 0
}

code_changed() {
  [ ! -x "$binary" ] && return 0
  [ -n "$(find main.go app.go go.mod go.sum cmd internal frontend/src frontend/index.html frontend/package.json \
    -type f -newer "$binary" -print -quit)" ]
}

find_wails() {
  need go "Install it from https://go.dev/dl/"
  need npm "Install Node from https://nodejs.org/"

  wails=$(command -v wails || true)
  if [ -z "$wails" ]; then
    wails=$(go env GOPATH)/bin/wails
    if [ ! -x "$wails" ]; then
      echo "Installing Wails $wails_version"
      go install "github.com/wailsapp/wails/v2/cmd/wails@$wails_version"
    fi
  fi
}

run_app() {
  find_wails
  if code_changed; then
    echo "Building the app"
    "$wails" build
  fi

  # open starts apps through launchd, which doesn't pass on this shell's variables.
  if [ -n "${KUBECONFIG:-}" ]; then
    open -n --env "KUBECONFIG=$KUBECONFIG" "$app"
  else
    open "$app"
  fi
}

run_dev() {
  need go "Install it from https://go.dev/dl/"
  need npm "Install Node from https://nodejs.org/"
  port_is_free "$api_port"
  port_is_free "$ui_port"

  [ -d frontend/node_modules ] || (cd frontend && npm install)

  # A built binary, because stopping `go run` can leave its child running.
  mkdir -p build/bin
  go build -o build/bin/kubehack-dev ./cmd/kubehack-dev

  local token ui_url=http://localhost:$ui_port
  token=$(openssl rand -hex 16)
  build/bin/kubehack-dev -port "$api_port" -origin "$ui_url" -token "$token" >/dev/null &
  local api=$!
  trap 'kill "$api" 2>/dev/null || true' EXIT

  (
    until curl -s -o /dev/null "$ui_url"; do sleep 0.3; done
    open "$ui_url"
  ) &
  local browser=$!
  trap 'kill "$api" "$browser" 2>/dev/null || true' EXIT

  # The API only accepts this port, so Vite must not pick another.
  cd frontend
  VITE_KUBEHACK_API=http://127.0.0.1:$api_port VITE_KUBEHACK_TOKEN=$token \
    npx vite --port "$ui_port" --strictPort
}

run_release() {
  find_wails
  mkdir -p releases

  # One build covers both Apple silicon and Intel Macs.
  "$wails" build -clean -platform darwin/universal
  rm -f releases/kubehack-macos.zip
  # ditto keeps the bundle's signature, which zip can break.
  ditto -c -k --keepParent "$app" releases/kubehack-macos.zip

  "$wails" build -platform windows/amd64
  rm -f releases/kubehack-windows-x64.zip
  (cd build/bin && zip -q ../../releases/kubehack-windows-x64.zip kubehack.exe)

  ls -lh releases
}

open_url() {
  if command -v open >/dev/null; then open "$1"; else xdg-open "$1"; fi
}

settings_dir() {
  if [ "$(uname)" = Darwin ]; then
    echo "$HOME/Library/Application Support/kubehack"
  else
    echo "${XDG_CONFIG_HOME:-$HOME/.config}/kubehack"
  fi
}

run_docker() {
  need docker "Install Docker Desktop from https://www.docker.com/products/docker-desktop/"
  docker info >/dev/null 2>&1 || fail "Docker isn't running. Start it, then try again."
  port_is_free "$docker_port"

  local files file index=0 kubeconfig="" mounts=() target=kubehack image=kubehack
  IFS=: read -ra files <<<"${KUBECONFIG:-$HOME/.kube/config}"
  for file in "${files[@]}"; do
    [ -f "$file" ] || continue
    mounts+=(-v "$file:/kube/$index:ro")
    kubeconfig+="${kubeconfig:+:}/kube/$index"
    index=$((index + 1))
    grep -q gke-gcloud-auth-plugin "$file" && target=kubehack-gke
  done
  [ -n "$kubeconfig" ] || fail "No kubeconfig found. Check that kubectl get pods works, or set KUBECONFIG."

  # GKE sign-in needs gcloud, which makes the image much bigger. gcloud fails on
  # a read-only config directory, even when it has nothing to change.
  if [ "$target" = kubehack-gke ]; then
    image=kubehack:gke
    [ -d "$HOME/.config/gcloud" ] || fail "No gcloud sign-in found. Run gcloud auth login, then try again."
    mounts+=(-v "$HOME/.config/gcloud:/gcloud")
  fi

  docker build --target "$target" -t "$image" .

  # Shared with the desktop app, so a cluster marked read-only stays read-only.
  local settings
  settings=$(settings_dir)
  mkdir -p "$settings"

  # An exported variable, because a value on the command line shows up in ps.
  local url=http://localhost:$docker_port
  KUBEHACK_TOKEN=$(openssl rand -hex 32)
  export KUBEHACK_TOKEN

  (
    until curl -s -o /dev/null "$url"; do sleep 0.3; done
    open_url "$url/#token=$KUBEHACK_TOKEN"
  ) &
  # Expanded now, because the local variable is gone by the time the script exits.
  local browser=$!
  trap "kill $browser 2>/dev/null || true" EXIT

  # The port stays the same inside, because the API checks it against Host. Only
  # this computer may reach it: anyone holding the token has your cluster access.
  docker run --rm --init \
    --user "$(id -u):$(id -g)" \
    -p "127.0.0.1:$docker_port:$docker_port" \
    -e KUBEHACK_TOKEN -e KUBECONFIG="$kubeconfig" -e KUBEHACK_DEBUG \
    "${mounts[@]}" \
    -v "$settings:/settings" \
    "$image" -port "$docker_port"
}

case "${1:-}" in
  "") run_app ;;
  dev) run_dev ;;
  release) run_release ;;
  docker) run_docker ;;
  *) fail "Usage: ./run.sh [dev|release|docker]" ;;
esac
