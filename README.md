# pnop

`pnop` forwards every command to `pnpm`. If a command fails, pnop compares the npm token in your `.npmrc` against 1Password. When the token is stale it rewrites the file and reruns the command, and when the token is already current it leaves the original error alone. Interactive prompts from pnpm and corepack pass straight through, so it behaves like plain `pnpm` in every other respect.

## Install

```sh
brew install --cask frodi-karlsson/tap/pnop
```

## Setup

```sh
pnop setup -c work --file=~/.npmrc --vault=MyVault --item="My item" --field=MyField
```

`--field` names the key on the item that holds the token, because pnop assumes nothing about how your vault is arranged. That key's value can be either the bare token or the whole `//registry.npmjs.org/:_authToken=<token>` line. Repeat `setup` with a different `-c` name to define a second config, then switch between them with `pnop setup -c <name>`, which rewrites the npmrc and makes that config active. Everything else is plain pnpm, so `pnop install`, `pnop up --latest` and `pnop run build` all work. Setup is only needed for token recovery, so pnop acts as a normal pnpm alias before you configure anything.
