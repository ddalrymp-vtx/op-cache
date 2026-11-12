# op-cache

A drop-in `op` shim that caches secret values on local disk, so `op read` keeps
working when the 1Password CLI will not connect to the desktop app.

On a cache hit the real `op` is never started: no desktop integration, no
connection to flake out, and a read takes about 18 ms instead of ~1.5 s.
Everything else — `op item`, `op vault`, `op signin`, `op run`, `op inject` —
is handed straight to the real CLI untouched.

> **This is not secure.** Cached secret values are stored in the clear under
> `~/.op-cache`, readable by your user account. It is a convenience for local
> development, deliberately traded against secrecy. Do not use it on shared
> machines, on anything production-adjacent, or for credentials you would not
> be comfortable leaving in a plaintext file in your home directory.

## Install

```sh
./install.sh
```

This builds the shim to `~/dev/op`. That directory is already ahead of
`/opt/homebrew/bin` on your PATH, so `op` resolves to the shim and the shim
finds Homebrew's `op` behind it. Verify:

```sh
op cache status
```

```
op-cache version:    0.1.0
Cache directory:     /Users/you/.op-cache
Cached entries:      0
Real 1Password CLI:  /opt/homebrew/bin/op
Status:              active
```

To install elsewhere: `PREFIX=~/.local/bin ./install.sh` (that directory must
come before Homebrew on PATH). To remove it: `make uninstall`, or delete
`~/dev/op`.

## Use

Nothing changes in how you call `op`:

```sh
op read op://Employee/my-item/api_key     # first time: fetches, caches
op read op://Employee/my-item/api_key     # after that: served from disk
```

The first read of a given reference goes to 1Password and stores the result.
Every later read of that reference is served locally, including when the
desktop integration is refusing connections.

### Warming the cache

The useful habit: while `op` is working, pre-load the references you rely on.

```sh
op cache warm \
  op://Employee/my-item/api_key \
  op://Employee/db/password
```

Then they are there when it stops working.

## Managing the cache

| Command | What it does |
| --- | --- |
| `op cache list` | List cached references (values hidden) |
| `op cache list --reveal` | List them with values shown |
| `op cache get <ref>` | Print one cached value |
| `op cache set <ref> [value]` | Store a value by hand; reads stdin if omitted |
| `op cache warm <ref>...` | Fetch from 1Password now and cache |
| `op cache rm <ref>...` | Drop specific entries |
| `op cache clear` | Drop everything (prompts; `--force` skips) |
| `op cache status` | Location, entry count, which real `op` is in use |
| `op cache path` | Print the cache directory |

Entries never expire. When a secret rotates, the cache is wrong until you say
otherwise:

```sh
op cache rm op://Employee/my-item/api_key   # next read refetches
```

## Environment variables

| Variable | Effect |
| --- | --- |
| `OP_CACHE_DIR` | Cache location (default `~/.op-cache`) |
| `OP_CACHE_REAL_OP` | Explicit path to the real `op`, skipping PATH discovery |
| `OP_CACHE_DISABLE=1` | Bypass the cache entirely; every command goes to the real `op` |
| `OP_CACHE_DEBUG=1` | Trace cache hits and misses on stderr |

`OP_CACHE_DISABLE=1 op read ...` is the quickest way to confirm whether a
surprising value is coming from the cache or from 1Password.

## What is and is not cached

Cached:

- `op read op://vault/item/field` and its `-n`, `-o/--out-file`, `--file-mode`,
  and `-f/--force` flags.
- Entries are keyed by reference **and** account, so `--account work` and
  `--account personal` never see each other's values.

Never cached:

- One-time passwords (`?attribute=otp`). A cached TOTP is wrong within 30
  seconds, so those always go to the real `op`.
- Failed reads. An error is never stored, so a lookup that failed because
  1Password was down will be retried next time.
- Every other subcommand. `op run`, `op inject`, `op item get`, and the rest
  are pure passthrough and still need a working 1Password connection.

Note that `op run` and `op inject` resolve references internally in the real
CLI, so the cache does not help them. If you need a secret during an outage,
`op read` it and pass the value in yourself.

## How it works

1. `op` on PATH is this shim.
2. `op cache ...` is handled locally. Any subcommand other than `read` is
   `exec`'d straight into the real `op` — same pid, same signals, same exit
   code, same stdio.
3. For `op read` with a single cacheable reference, the shim checks
   `~/.op-cache/entries/<sha256>.json`. A hit is printed directly. A miss runs
   the real `op read -n`, stores the result, and prints it.
4. The real `op` is found via `OP_CACHE_REAL_OP`, else the first `op` on PATH
   that is not this binary. A depth counter catches a shim that somehow
   resolves to itself rather than forking forever.

Anything the shim does not fully understand — unknown flags, `--help`, two
references, a malformed reference — is forwarded to the real `op` rather than
guessed at.

Each entry is one JSON file written atomically with mode `0600` in a `0700`
directory:

```json
{
  "reference": "op://Employee/my-item/api_key",
  "value": "the-actual-secret",
  "encoding": "plain",
  "created_at": "2026-11-12T10:51:02-05:00",
  "updated_at": "2026-11-12T10:51:02-05:00",
  "source": "op"
}
```

Values are stored as plain text when they are valid UTF-8 and base64 otherwise,
so you can read and hand-edit the ordinary cases.

## Uninstall

```sh
make uninstall          # removes ~/dev/op
rm -rf ~/.op-cache      # removes the cached secrets
```

## Development

```sh
make test    # unit + end-to-end tests
make build   # builds to ./bin/op
```

The end-to-end tests build the shim and run it against a fake `op` that counts
its own invocations, which is how the suite proves a cache hit never starts the
real CLI, and that a warmed reference still resolves when `op` is failing.
