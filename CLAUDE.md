# agent-web

A browser UI that drives several CLI coding agents — Claude Code, Codex, OpenCode —
through one protocol. Go backend (`backend/`), React + Vite frontend (`frontend/`).
See [README.md](README.md) for the architecture.

## Git workflow

**Commit straight to `main` and push, unless a branch or PR was explicitly asked for.**
This is a single-author repo; do not open a branch "to be safe".

## Checks before committing

```bash
cd backend && go build ./... && go vet ./... && go test ./... && gofmt -l .
```

```bash
cd frontend && pnpm lint && pnpm build
```

gofmt and `go vet` also run on commit through prek: `uvx prek install` once per clone.

Live harness tests cost a model call each and are opt-in:

```bash
cd backend && AGENT_WEB_E2E=1 go test ./internal/server/ -run TestClaudeCodeTurn -v
```

## The rule the whole design rests on

**The protocol is kind-neutral, and both sides honour that.**

- `internal/protocol` carries no harness types. Every enum member has a comment
  naming the native value it maps to; that comment is the only place a harness
  name belongs in that package.
- The **service** applies every kind-neutral policy exactly once — cwd
  validation, mode/effort checks, image limits, prompt idempotency, decision
  matching, the steer gate. A backend that re-checks one of these is wrong; a
  backend that needs a new check means the check belongs in the service.
- Each command is its own `chat.Backend` method. Adding one must fail to compile
  in every adapter that has not implemented it — never fall through a switch.
- Only `Prompt` may start a harness process. Everything else answers from stored
  state, and a process that died is resumed by the next prompt, never surfaced
  as a run state.
- In the frontend, `agent_kind` may only decide the identity chip in
  `AgentMark.tsx`. Every control is rendered from the agent's descriptor; a
  `if (kind === 'claude_code')` anywhere else is a defect. If the UI needs to
  know something about a harness, the descriptor should say it.

## Go

- Standard library only. No router, no JSON codegen, no DI framework. If a
  dependency looks necessary, say why before adding it.
- Comments say **why**, and only where the reason is not on the line above.
  Never narrate what the code does.
- Errors the client sees are `*protocol.Error` with a code from the closed list;
  anything else reaching a handler is a bug and becomes a 500.
- A mutex protects state, not calls. Never hold a lock across a call that can
  re-enter the same structure — the registry composes summaries outside its own
  lock for exactly that reason.
- Tests are for business rules, not for lines of code. `internal/chat` tests the
  policy every adapter depends on; adapters are proven against the real
  harnesses by the opt-in e2e test rather than by mocks of their wire protocol.

## Frontend

- React 19, Tailwind v4, zustand, oxlint. Types in `store/protocol.ts` mirror
  `internal/protocol` by hand; keep them in step.
- Colour means state. `running` / `waiting` / `failed` are the only hues in the
  chrome, each agent keeps its vendor hue for identity alone, and everything
  else is paper and ink. Do not introduce a decorative colour.
- The transcript reducer folds live events and replayed history through the same
  function; if the two ever need different handling, the protocol is wrong.
