# Agent CLIs: the app's own copy, updates and sign-in

The three agents (claude, codex, agy) are run from a **private copy the app keeps itself**. Nothing
installed elsewhere on the machine is read, changed or replaced.

## Where things live

| What | Where |
|---|---|
| Managed CLI copies | `<data>/agent-cli/<provider>/versions/<version>/` + `current.json` (the version in use) |
| Accounts / sessions / settings | `<data>/agent-profiles/<provider>/` (isolated: `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, agy profile) |

Executable order: **custom path (user's choice) → managed copy → a CLI found on the machine**. A CLI on
the machine is only a fallback until the managed copy has been downloaded.

Not done on purpose: running the vendors' `install` commands (they edit `PATH` and shell setup), writing
to `~/.claude`, `~/.codex`, `%LOCALAPPDATA%\agy`, or signing the user's own CLI out. The CLIs' own updaters
are switched off for the app's copy (`InstallSpec.Env`).

## Install and update

`internal/service/agentcli` downloads the newest release from the vendor's official channel, verifies its
checksum (SHA-256 or SHA-512 as the channel publishes), unpacks it next to the old version and only then
switches `current.json`. A failed or tampered download never becomes current; the previous version stays on
disk for rollback (`Keep`, default 2).

The facts of each channel are declared once in `domain/agent/agent.go` (`ProviderSpec.Install`): scheme,
base URL, platform tokens, executable path. To add a CLI, add its spec; to add a new kind of channel, add a
scheme in `agentcli/release.go`.

Config (`agent.*`): `auto_install_cli`, `auto_update_cli`, `cli_update_interval`.
`Service.ProvisionCLIs` runs in the background at start-up and then every interval; progress reaches the UI
as `ProviderInfo.Installing` / `InstallPercent`.

Live check against the real channels (network, several hundred MB):
`AGENTCLI_LIVE=1 go test ./internal/service/agentcli -run Live -v`

## Sign-in

`ProviderSpec.Login` declares the sign-in command and whether it waits for a pasted code.

- **codex** — `codex login --device-auth`: the UI shows the page and the one-time code to type there.
- **claude** — `claude auth login`: the CLI opens the page; the UI shows a field for the code the page shows.
- **agy** — Google sign-in over ACP, or a separate Gemini API key. agy keeps its Google token in the OS
  credential store, which the CLI controls and the app cannot isolate; signing out in the app only forgets
  the app's own marker and never signs the user's agy out.

`login_interactive.go` parses the CLI's output for the page and code (`agent:login_prompt` event), writes a
pasted code to the CLI's stdin (`agent:login_code`), and cancels (`agent:login_cancel`).
