#!/usr/bin/env bash
#
# ./run.sh        Build the app if the code changed, then open it.
# ./run.sh dev    Run the API and the UI with hot reload, in a browser.
#
# To limit a run to certain clusters, set KUBECONFIG first.
set -euo pipefail
cd "$(dirname "$0")"

wails_version=v2.17.1
app=build/bin/kubehack.app
binary=$app/Contents/MacOS/kubehack
api_port=8765
ui_port=5173

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

run_app() {
  need go "Install it from https://go.dev/dl/"
  need npm "Install Node from https://nodejs.org/"

  local wails
  wails=$(command -v wails || true)
  if [ -z "$wails" ]; then
    wails=$(go env GOPATH)/bin/wails
    if [ ! -x "$wails" ]; then
      echo "Installing Wails $wails_version"
      go install "github.com/wailsapp/wails/v2/cmd/wails@$wails_version"
    fi
  fi

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

case "${1:-}" in
  "") run_app ;;
  dev) run_dev ;;
  *) fail "Usage: ./run.sh [dev]" ;;
esac
