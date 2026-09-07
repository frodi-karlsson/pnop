# pnop

`pnop` forwards every command to `pnpm`. When a command fails, it asks the registry whether your npm token is still accepted, and refreshes it from 1Password when it is not.

## Install

```sh
brew install --cask frodi-karlsson/tap/pnop
```

The cask depends on `1password-cli`, so brew installs that too. For biometric unlock rather than a password prompt, turn on "Integrate with 1Password CLI" in the desktop app.

The cask ships the `pnop` binary. Routing `pnpm` through it is up to you. A shell alias covers what you type:

```sh
alias pnpm=pnop
```

Scripts calling `pnpm`, package.json scripts included, never see that alias. A shim earlier in PATH covers both, and has to name the real pnpm or it resolves back to itself:

```sh
#!/bin/sh
PNOP_PNPM=/opt/homebrew/bin/pnpm exec pnop "$@"
```

## Setup

Point pnop at the 1Password item that holds your npm token:

```sh
pnop +setup -c work --vault=MyVault --item="My item" --field=MyField
```

`--field` names the key on the item that holds the token, because pnop assumes nothing about how your vault is arranged. That key's value can be either the bare token or a whole `//registry.npmjs.org/:_authToken=<token>` line.

Setup probes what it fetched and reports who it belongs to:

```
[pnop] registry.npmjs.org accepts this token right now, as frodi
```

Right now is the only claim. Every npm token expires: granular tokens carry a mandatory expiry, and `npm login` writes a session token that dies within the day, so an item holding one of those makes almost every command prompt. Setup also warns when the npmrc it manages names a different registry than the config does.

Passing any flag replaces a config of the same name outright, so a field you leave out returns to its default rather than to what was there before.

`--file` defaults to `~/.npmrc`, which is what pnpm reads. Pass it only if your token lives elsewhere. Setup is needed for recovery alone, so pnop works as a plain pnpm alias before you configure anything.

To rewrite the npmrc from 1Password on demand, without waiting for a command to fail:

```sh
pnop +refresh
```

pnop's own commands carry a `+`: `+setup`, `+refresh`, `+version`, `+help`. Everything without it is pnpm's, which matters because pnpm has a `setup` of its own, and because a repo script named `refresh` stays reachable as plain `pnpm refresh`.

### Switching between tokens

Define a second config, then switch with `-c` alone:

```sh
pnop +setup -c personal --vault=MyOtherVault --item="My other item" --field=MyField
pnop +setup -c work
```

Each switch rewrites the npmrc with that config's token. Drop one you no longer want with `pnop +setup -c personal --remove`, which deletes the config and leaves the npmrc alone.

## How it works

Every command goes straight to pnpm. If it succeeds, pnop does nothing at all: no config read, no registry contacted, no vault touched.

If it fails, pnop asks `GET <registry>/-/whoami` with the token from your npmrc. That is a question about the credential, answered by the registry, rather than an inference from what pnpm printed, which changed with every pnpm major while the fix stayed the same.

| whoami answers | What pnop does |
| - | - |
| 200 | Nothing. The token works, so the failure is something else. |
| 401 | Reads 1Password, and writes the token if the vault holds a live one. |
| 403, 404, 405, 5xx | Nothing. That describes the endpoint, not your token. |
| nothing at all | Nothing. Being offline is not evidence about a credential. |

With no token in the npmrc, pnop skips the probe and reads 1Password directly, since an unauthenticated whoami answers 401 about a credential that does not exist.

A refresh does not rerun your command. pnop cannot see whether the first attempt got far enough to have a side effect, so it writes the token and hands the command back:

```
[pnop] refreshed the npm token in /Users/you/.npmrc
[pnop] run it again: pnpm add @scope/pkg
```

## Deliberate no-ops

- **No rerun by default.** Opt in with `--rerun` at setup, `rerun = true` in the config, or `PNOP_RERUN=1` once. The rerun carries `PNOP_RETRIED=1` in its own environment, so one refresh is the budget even when a pnpm script calls pnpm.
- **A valid but too narrow token is not recovered.** If the package you asked for is outside its grants, whoami answers 200 while pnpm answers 404: identity and authorization are different questions. Run `pnop +setup -c <name>` to refetch.
- **Only the registry your config names is probed.** A config holds one registry and one vault field, so a 401 from another host could only be answered with a credential that does not belong to it. Use one config per registry.
- **Repeated failures do not repeat the prompt.** When a vault read cannot help, pnop remembers that for ten minutes under `~/Library/Caches/pnop` and says so instead of prompting again.
- **`pnpm -r` reports pnpm's own exit code**, not the script's, with or without `--no-bail`.
- **pnpm caches registry metadata for 24 hours**, per package and registry, by the cache file's mtime. Inside that window `install` and `up` make no registry request, so a dead token cannot fail a command and pnop has nothing to react to. This is the usual reason for "pnop didn't fire".

## Rerunning from a wrapper

Set `PNOP_REFRESH_MARKER` to a path your wrapper owns and pnop touches it after a refresh:

```sh
pnpm() {
    PNOP_REFRESH_MARKER="${TMPDIR:-/tmp}/pnop.$$.marker" pnop "$@"
    ...
}
```

pnop never picks that path itself. A fixed one is shared between shells, where one shell's refresh would make another rerun an unrelated command.
