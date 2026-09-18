# agent-web

A remote, browser-based UI for the coding agents installed on one machine.
Today that is [Claude Code](https://docs.anthropic.com/en/docs/claude-code),
[Codex](https://developers.openai.com/codex/cli),
[OpenCode](https://opencode.ai) and [Pi](https://github.com/earendil-works/pi) —
different processes, different wire protocols, one chat.

Single user, no accounts. Run it on localhost or behind a private network / VPN.

```
┌──────────────────────┐  REST  /api/...                 ┌──────────────────────────────────┐
│ frontend             │ ──────────────────────────────► │ backend (Go, stdlib only)        │
│ React / Vite         │  SSE   /api/threads/{id}/events │   Service ── kind-neutral policy │
│                      │ ◄────────────────────────────── │     │        + one thread list   │
│                      │  SSE   /api/events (directory)  │     ├── claudecode ── claude ────┼─► stream-json stdio
└──────────────────────┘                                 │     ├── codex ─────── codex ────┼─► JSON-RPC stdio
                                                         │     ├── opencode ──── opencode ─┼─► HTTP + SSE
                                                         │     └── pi ────────── pi ───────┼─► JSON lines stdio
                                                         └──────────────────────────────────┘
```

## Why one protocol

Every harness has its own idea of a conversation: Claude Code is one subprocess
per session speaking newline JSON, Codex is a single `app-server` multiplexing
every thread over JSON-RPC, OpenCode is an HTTP server you talk to like any
other API, Pi is again one subprocess per session, answering each command by
the id it was sent with. Behind all four is the same shape — a turn, streamed text and
reasoning, tool calls with results, and the moments where the agent stops and
asks you something.

The backend models exactly that shape and adapts each harness to it. The result
is that switching agents changes what the descriptor offers, never how the app
works: the same transcript, the same permission card, the same composer.

## Features

- Streamed text, reasoning and tool cards, with a subagent's own work nested
  under the call that started it
- Permission prompts, questions and plan approvals, answered from the browser
- Per-thread model and whatever knobs the agent itself has — chosen when the
  thread is created, changed any time after, and named and valued the way that
  agent names and values them
- Interrupt a running turn; steer one mid-flight where the harness supports it
- A context ring in the composer, holding the window, the per-turn cost and
  what fills it, where the harness reports them
- Paste, drop or pick images into a message
- Slash-command autocomplete and `@` file search, both from the server
- Pick a working directory per thread: type a path, or browse the machine from
  the folder button beside it
- Idle harness processes are stopped and transparently resumed on the next prompt
- Optional single-password login covering the REST API and the event streams

## Prerequisites

- Go 1.24 (`go.mod` will fetch the toolchain if your `go` is older)
- Node 22 and [pnpm](https://pnpm.io/)
- At least one agent CLI, signed in:

| Agent | Install | Sign in | Check | Built against |
| --- | --- | --- | --- | --- |
| Claude Code | `npm i -g @anthropic-ai/claude-code` | `claude login` | `claude auth status` | 2.1.270 |
| Codex | `npm i -g @openai/codex` | `codex login` | `codex doctor` | 0.153.2 |
| OpenCode | `npm i -g opencode-ai` | `opencode auth login` | `opencode models` | 1.18.31 |
| Pi | `npm i -g @earendil-works/pi-coding-agent` | `pi`, then `/login` | `pi --list-models` | 0.85.1 |

An agent that is missing or signed out still appears in the UI, greyed out with
the reason — the others keep working.

**Built against** is the version each adapter was written and verified on. None
of the four publishes a stable wire protocol: flags, JSON-RPC notification
names and event fields move between releases, and the adapters read them
directly. So a much newer CLI usually keeps working and loses one specific thing
quietly — reasoning stops appearing, a tool card stops being drawn — rather than
failing outright. When something goes missing after an upgrade, suspect the
adapter for that kind first; `AGENT_WEB_E2E=1 go test ./internal/server/ -v`
drives Claude Code against the real CLI.

## Quick start

Two processes, hot reload:

```bash
cd backend && cp .env.example .env && go run ./cmd/agentweb
```

```bash
cd frontend && pnpm install && pnpm dev
```

Open http://localhost:5173. Vite proxies `/api` to the backend on port 8000
(override with `BACKEND_URL`).

One process:

```bash
cd frontend && pnpm install && pnpm build
```

```bash
cd backend && go run ./cmd/agentweb
```

The backend then serves `frontend/dist` alongside the API on
`AGENT_WEB_HOST:AGENT_WEB_PORT` (default `127.0.0.1:8000`).

## Starting a thread

The new-thread dialog asks three things: which agent, how it should run, and
where. The options row is drawn from the chosen agent's descriptor — its model
list and its own knobs — so an agent with no reasoning knob shows one control
fewer, and picking a different agent redraws the row instead of carrying over a
value that means nothing to it. The knobs are the harness's: Claude Code offers
`permission-mode` and `effort`, Codex offers `approvalPolicy` and `sandbox`
separately because that is how the app-server asks, OpenCode offers the
`agent` list its own config defines, and Pi offers `thinking`, the level
`pi --thinking` takes. Every option defaults to what that agent
reports and can be changed later from the composer.

The working directory is the one choice a thread cannot revisit, so the dialog
states it plainly: type a path, or open the folder button to browse the machine
the server runs on. Whichever directory you stop in is the one the thread gets.

## Conversation display

Consecutive tool calls and reasoning are grouped into a collapsed activity
summary, even when they arrive in separate agent messages. Open the summary to
browse individual calls, then open a call to see its input, output, images and
subtasks. Replies, user messages, errors and approval requests remain visible.
Activity summaries show running, failed and missing-result states, and carry the
newest line of reasoning they hold — while the turn runs that reads as a live
status line, and it is the only reasoning a collapsed group would otherwise
show. Reasoning previews use an excerpt of the original text. All controls
support Chinese and English and can be expanded with the keyboard.

## Interface language

The frontend supports Simplified Chinese and English. The selector lives in
settings, opened from the sidebar footer (and stands on its own on the sign-in
and connection-error screens, which have no sidebar). The first visit follows the browser's supported language preferences,
falling back to English; an explicit choice is saved locally for future visits.
Switching languages preserves the current conversation and unsent draft.

UI translations live in `frontend/src/i18n/en.ts` and `zh.ts`, with shared typed
keys. Conversation content, paths, model names and raw server diagnostics stay
in their original language.

## Configuration

Environment variables prefixed `AGENT_WEB_`, read from the environment or from a
`.env` file in the backend's working directory. See
[backend/.env.example](backend/.env.example).

| Variable | Default | Purpose |
| --- | --- | --- |
| `AGENT_WEB_ROOT_DIR` | `~/agent-web-workspace` | Default working directory for new threads; the picker can choose any other |
| `AGENT_WEB_DATA_DIR` | `~/.agent-web` | Thread registry and auth secret |
| `AGENT_WEB_AGENTS` | all | Comma-separated kinds to expose, in order: `claude_code,codex,opencode,pi` |
| `AGENT_WEB_CLAUDE_PATH` / `CODEX_PATH` / `OPENCODE_PATH` / `PI_PATH` | on `PATH` | Explicit harness binaries |
| `AGENT_WEB_CLAUDE_OAUTH_TOKEN` | unset | Token from `claude setup-token`, when there is no `claude login` |
| `AGENT_WEB_IDLE_TIMEOUT_S` | `900` | Seconds before an idle harness process is stopped |
| `AGENT_WEB_HOST` / `AGENT_WEB_PORT` | `127.0.0.1` / `8000` | Bind address |
| `AGENT_WEB_STATIC_DIR` | `../frontend/dist` if present | Built frontend to serve |
| `AGENT_WEB_PASSWORD` | unset | Password for the web UI; unset disables the login screen |
| `AGENT_WEB_AUTH_TTL_S` | `2592000` (30 days) | How long a login lasts |
| `AGENT_WEB_COOKIE_SECURE` | auto | Force `Secure` on the auth cookie; auto = on over https |
| `AGENT_WEB_ALLOWED_ORIGINS` | unset | Exact origins allowed to call the API; unset = same host, plus loopback when bound to loopback |
| `AGENT_WEB_ALLOW_NO_AUTH` | `false` | Permit binding a non-loopback host with no password |

### Login

Set `AGENT_WEB_PASSWORD` and restart. A successful sign-in stores an
HMAC-signed token in an httpOnly, SameSite=Lax cookie, which authenticates REST
calls and the SSE streams alike — an `EventSource` cannot attach headers, so a
cookie is the only credential that covers both. Failed logins are rate-limited
per IP and globally.

The signing key is derived from the password and a random per-install key in
`AGENT_WEB_DATA_DIR/auth_secret`, so **changing the password signs every browser
out** while a restart does not.

Leaving the password unset disables the gate, which is only allowed on a
loopback host: with no password on any other address the server refuses to
start, since every thread runs with your shell and filesystem access. Override
with `AGENT_WEB_ALLOW_NO_AUTH=true` only when the port is protected some other
way (VPN, SSH tunnel, authenticating reverse proxy).

## API

| Method | Path | Description |
| --- | --- | --- |
| GET | `/api/auth/status` | Public. Whether a password is required and whether this browser is signed in |
| POST | `/api/auth/login` / `logout` | Public. Sets or clears the auth cookie |
| GET | `/api/config` | Default working directory and home directory |
| GET | `/api/agents` | One descriptor per agent: capabilities, models, commands, defaults, why it is unavailable |
| GET | `/api/fs/dirs` | Sub-directories of `?path=`, for the work-directory picker |
| GET | `/api/events` | SSE. `thread_updated` and `thread_deleted` for every thread |
| GET / POST | `/api/threads` | The directory (keyset `cursor`) / create a thread |
| GET / PATCH / DELETE | `/api/threads/{id}` | Detail (pending interactions, context usage, last turn) / rename / delete |
| GET | `/api/threads/{id}/messages` | Settled transcript, paged backwards with `before` |
| GET | `/api/threads/{id}/events` | SSE. Increments only |
| POST | `/api/threads/{id}/input` | `prompt`, `interrupt`, `steer`, `set_options`, `interaction_response`. Always 204 |
| GET | `/api/threads/{id}/files/search` | `@` panel, scoped to the thread's directory |

A thread id is `<agent_kind>:<id>`, so the server routes on a prefix and never
looks up ownership. Commands return 204 and nothing else: everything a command
produces arrives on the event stream.

**Connect in this order: open `/events`, then GET the detail, then GET the
messages.** Events that arrive before the fetches can only be duplicates, which
the client drops by id. The other order loses whatever lands in the gap — which
is exactly where a permission prompt would be.

## How it works

- [backend/internal/protocol](backend/internal/protocol) — the shared vocabulary.
  Closed enums with a comment on each member naming the native value it maps to;
  18 server events; 5 client commands; 3 interaction kinds.
- [backend/internal/chat](backend/internal/chat) — `Service` dispatches by kind
  and is the single place kind-neutral policy lives. `Registry` is the SQLite
  database of threads this UI created, so the directory never lists a session
  someone started in a terminal; `ThreadRecord` is both the row and the mapped
  model, and the schema is migrated on every start. `Hub` fans events out to
  per-thread and directory subscribers.
- [backend/internal/agents/claudecode](backend/internal/agents/claudecode) —
  `claude --print --input-format stream-json` plus the control channel
  multiplexed onto the same pipes (`initialize`, `interrupt`, `set_model`,
  `get_context_usage`, and the CLI's `can_use_tool` prompts). One subprocess per
  live thread, reaped when idle, resumed with `--resume`. `--thinking-display
  summarized` is what makes reasoning arrive with text in it.
- [backend/internal/agents/codex](backend/internal/agents/codex) — one
  `codex app-server` for every thread, JSON-RPC over stdio, notifications routed
  by `threadId` and approvals answered as JSON-RPC responses. Started with
  `-c model_reasoning_summary=detailed`, without which the server streams no
  reasoning at all.
- [backend/internal/agents/opencode](backend/internal/agents/opencode) — a
  hosted `opencode serve`, one SSE subscription for all threads, `?directory=`
  scoping each call to its thread's project.
- [backend/internal/agents/pi](backend/internal/agents/pi) — `pi --mode rpc`,
  one subprocess per live thread, one JSON object per line each way, every
  command answered by id. A new thread pins pi's session id to its own; a
  reaped thread reopens the session file pi named with `--session`, and
  history is read straight from that file.
- [frontend/src/store/transcript.ts](frontend/src/store/transcript.ts) — folds
  live events and replayed history through one function, which is why a reload
  renders exactly what the stream did.

Three design decisions worth calling out:

1. **A directory stream.** `thread_updated` could have lived only on a thread's
   own stream, but then a browser sidebar would have to poll, so `/api/events`
   carries directory events for every thread at once.
2. **One registry, not one per backend.** Each harness can list its own threads
   natively, but the directory only ever shows threads this UI created, so a
   single shared registry replaces four near-identical implementations and
   makes the union list a sort rather than a merge.
3. **A single-password gate instead of per-user identity**, and `cwd` validated
   as "absolute, exists, is a directory" rather than against a fixed set of
   project roots.

## Notes and caveats

- Capabilities are not the same across agents, and the UI says so rather than
  pretending: Codex has no plan mode and Claude Code cannot steer a running
  turn, so neither control is drawn for them. OpenCode has no reasoning-effort
  knob, and reports a token count without a window size, so it gets a count
  instead of a ring. Knob names are not translated either — a control says
  `bypassPermissions` because that is the word the CLI takes, and a word that
  reads the same in the UI and in `claude --help` is worth more than a pretty
  one that reads the same on no harness at all.
- Pi has no tool approval of its own: `read`, `bash`, `edit` and `write` run
  with the backend's permissions the moment the model calls them, in the
  browser exactly as in a terminal, so a Pi thread never shows a permission
  card. Dialogs raised by Pi extensions the owner has installed (`select`,
  `confirm`, `input`, `editor`) do reach the browser, as questions.
- One turn runs per thread at a time. A prompt sent while a turn is running is
  refused with `thread_busy`; press Stop first, or steer if the agent supports it.
- A prompt carries a ULID. Re-sending the same one is a no-op, and the same id
  with different content is a conflict — so a retry after a dropped connection
  cannot double a turn. The ledger lives in memory: a restart forgets it, which
  costs one duplicated turn at worst, never a lost one.
- A thread's working directory is fixed when it is created. Claude Code stores
  its transcript under a hash of that path and Pi under a folder named after
  it, so moving a thread would orphan its history.
- Live state is live. Pending interactions, context usage and the last turn's
  cost come from the running harness, so they are empty again after a server
  restart, while the transcript is not.
- Signing in grants the full access the user running the backend has, including
  `/api/fs/dirs`, which lists any directory that user can read.
- Serve over https (or a tunnel) if the port is reachable from anywhere but this
  machine: over plain http the password and cookie travel in the clear.
