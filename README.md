# pnop

`pnop` forwards every command to `pnpm`. When a command fails, it checks whether your npm token has gone stale, refreshes it from 1Password, and retries.

## How it works

Every command goes straight to pnpm. If it succeeds, pnop does nothing at all.

If it fails, pnop reads what pnpm printed. Unless pnpm reported a registry authentication problem, pnop stays out of the way completely, so a failing test suite, a type error or a missing script never reaches for your vault.

This is decided from the output rather than from the command, because the command cannot tell you. pnpm runs any package.json script as a bare subcommand, so `pnpm typecheck` and `pnpm upd` look identical from the outside even though one runs a compiler and the other runs `pnpm update`. Reading the output also means a registry failure is caught inside a script that wraps pnpm, which no amount of inspecting the arguments would find.

When pnpm did report an auth problem, pnop compares the npm token in your `.npmrc` against the one in 1Password:

| Token on disk | What pnop does |
| - | - |
| Matches 1Password | Nothing. The original error and pnpm's exit code pass through untouched. |
| Differs from 1Password | Rewrites the `.npmrc` with the fresh token, then reruns the command once. |

A rerun only happens when the token actually changed, so a command that failed for any other reason never runs twice.

Interactive prompts from pnpm and corepack pass straight through, so pnop behaves like plain pnpm in every other respect. Only `setup`, `--version` and `--help` belong to pnop. Everything else, including `help`, reaches pnpm untouched.

## Install

```sh
brew install --cask frodi-karlsson/tap/pnop
```

## Setup

Point pnop at the 1Password item that holds your npm token:

```sh
pnop setup -c work --vault=MyVault --item="My item" --field=MyField
```

`--field` names the key on the item that holds the token, because pnop assumes nothing about how your vault is arranged. That key's value can be either the bare token or a whole `//registry.npmjs.org/:_authToken=<token>` line.

`--file` defaults to `~/.npmrc`, which is the file pnpm reads. Pass it only if you keep your token elsewhere:

```sh
pnop setup -c work --vault=MyVault --item="My item" --field=MyField --file=~/.npmrc
```

Setup is only needed for token recovery, so pnop works as a plain pnpm alias before you configure anything.

## Switching between tokens

Define a second config under a different name:

```sh
pnop setup -c personal --vault=MyOtherVault --item="My other item" --field=MyField
```

Switch to it whenever you need it:

```sh
pnop setup -c personal
```

Switch back:

```sh
pnop setup -c work
```

Each switch rewrites the npmrc with that config's token and makes it the active one. Running `setup -c` against the config that is already active simply refetches the token, which is a convenient way to force a refresh.
