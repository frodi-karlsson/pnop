# pni

`pni` runs `pnpm install` and, when it fails, checks your npm token against 1Password — refreshing your `.npmrc` and retrying if the token was stale, and leaving the original error alone if it wasn't. Interactive prompts from pnpm and corepack pass straight through, so it behaves like `pnpm install` in every other respect. This is a personal, niche utility built around one specific workflow rather than something aimed at widespread use.

## Install

```sh
brew install frodi-karlsson/tap/pni
```

## Setup

```sh
pni setup --file=~/.npmrc --vault=MyVault --item="My item" --field=MyField
```

`--field` names the key on the item that holds the token; it is required, as pni assumes nothing about how your vault is arranged. That key's value may be either the bare token or the whole `//registry.npmjs.org/:_authToken=<token>` line — both are accepted. Then use `pni` wherever you'd type `pnpm install`; extra arguments are forwarded (`pni --frozen-lockfile`).
