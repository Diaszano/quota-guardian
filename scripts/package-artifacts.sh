#!/bin/sh
set -eu

dir=${1:?usage: package-artifacts.sh ARTIFACT_DIR}
[ -d "$dir" ] || { echo "artifact directory missing" >&2; exit 1; }
cd "$dir"
set --
for artifact in quota-guardian-*; do
	[ -f "$artifact" ] || continue
	case "$artifact" in
		quota-guardian-linux-amd64|quota-guardian-linux-arm64|quota-guardian-darwin-amd64-runtime-unsupported|quota-guardian-darwin-arm64-runtime-unsupported) set -- "$@" "$artifact" ;;
		*) echo "unsupported artifact name: $artifact" >&2; exit 1 ;;
	esac
done
[ "$#" -gt 0 ] || { echo "no release artifacts found" >&2; exit 1; }
sha256sum "$@" > SHA256SUMS
