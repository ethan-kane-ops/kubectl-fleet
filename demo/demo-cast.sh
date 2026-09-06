#!/usr/bin/env bash
# The scenario behind the asciinema recording on ethankane.net.
#
# Unlike demo.tape, which runs against demo/kubectl-fleet — a canned stub that
# prints the sample blocks from README.md — this drives three real kind clusters
# through the real binary. Every table on screen was merged from three live API
# servers during the take.
#
# That distinction is the point of the recording. A fan-out tool is only
# interesting if the fan-out actually happened, and a GIF of a stub cannot show
# that. The stub still earns its place: the README image has to be reproducible
# byte for byte, and three clusters are not.
#
#   just cast-setup   # three clusters and their fixtures — not recorded
#   just cast         # this script, under asciinema rec
#
# Only contexts matching CONTEXT_RE are ever queried. The recording is published
# and the operator's kubeconfig is not: without the filter, `contexts` would
# happily list every cluster on the machine.
set -euo pipefail

CONTEXT_RE="${CONTEXT_RE:-^kind-fleet-demo-}"
BIN="${BIN:-./bin/kubectl-fleet}"
NS="payments"

# Refuse to record if the filter would match anything that is not a local kind
# context. Checked before a frame exists, because a leak here means pulling the
# cast from the site and recording it again.
bad=$(kubectl config get-contexts -o name | grep -E "$CONTEXT_RE" | grep -v '^kind-' || true)
if [[ -n "$bad" ]]; then
  echo "refusing to record: filter matches non-kind contexts:" >&2
  echo "$bad" >&2
  exit 1
fi

type_line() {
  local line="$1" i
  printf '\033[38;5;108m$\033[0m '
  for ((i = 0; i < ${#line}; i++)); do
    printf '%s' "${line:i:1}"
    sleep 0.022
  done
  printf '\n'
}

# The command shown is the command that runs. Nothing is re-typed for the
# camera in a tidier form than what executes.
run() {
  type_line "$1"
  eval "$1"
  echo
}

note() {
  printf '\033[38;5;245m# %s\033[0m\n' "$1"
  sleep 1.4
}

clear

note "Three kind clusters, three real kubeconfig contexts."
note "Nothing here is merged ahead of time."
echo

run "$BIN contexts --filter '$CONTEXT_RE' --check"

note "Reachability is a live probe of each API server, not a cached list."
sleep 0.8
echo

run "$BIN status --contexts '$CONTEXT_RE' --since 10m"

note "One cluster is unhealthy. The fleet view is how you noticed."
sleep 1.2
echo

run "$BIN get deploy -n $NS --contexts '$CONTEXT_RE'"

note "One table, three API servers, queried in parallel."
sleep 2
