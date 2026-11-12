#!/usr/bin/env bash
# Install the op-cache shim ahead of the real 1Password CLI on PATH.
set -euo pipefail

PREFIX="${PREFIX:-$HOME/dev}"

cd "$(dirname "$0")"

# Look for the genuine op with PREFIX removed from PATH, so an already
# installed shim does not find itself.
stripped_path="$(echo "$PATH" | tr ':' '\n' | grep -vFx "$PREFIX" | paste -sd: -)"
real_op="$(PATH="$stripped_path" command -v op || true)"

if [ -z "$real_op" ]; then
  echo "error: could not find the real 1Password CLI on PATH." >&2
  echo "Install it first (brew install 1password-cli), then re-run this script." >&2
  exit 1
fi
echo "Real 1Password CLI: $real_op"

mkdir -p "$PREFIX"
go build -o "$PREFIX/op" .
echo "Installed shim:     $PREFIX/op"

hash -r 2>/dev/null || true
resolved="$(command -v op || true)"
echo "PATH resolves op:   $resolved"

if [ "$resolved" != "$PREFIX/op" ]; then
  cat >&2 <<MSG

WARNING: '$PREFIX' is not ahead of '$(dirname "$real_op")' on your PATH,
so the shim will not be used. Add this to your ~/.zshrc:

    export PATH="$PREFIX:\$PATH"

MSG
  exit 1
fi

echo
echo "Done. Verify with:  op cache status"
