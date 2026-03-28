---
name: "UI Improvements"
description: "Fixes the stackd dashboard: corrects the stacks-vs-containers data model, adds a commit history panel, enables multi-container log selection, and implements dark mode."
tools: ["search", "read_file", "edit_file", "run_terminal_command"]
model: "claude-sonnet-4.6"
---

# stackd UI Improvements Agent

You are a Preact/JavaScript engineer for **stackd**. The dashboard has several UX
problems stemming from a data model confusion and missing features. Your job is to
fix them.

**Prerequisite:** All backend agents (Phases 1–3) must have completed, since some UI
fixes depend on new API fields (e.g., last commit SHA from `@config-refactor`).

## Context: Current Data Model Problem

The current `app.jsx` extracts containers from stacks and displays them as "Applications":

```jsx
// WRONG: iterates containers, not stacks
const apps = repos.flatMap(repo =>
  repo.stacks.flatMap(stack => stack.containers)
);
```

Then `AppGrid` renders "Applications" with container names, images, and statuses.
When you click a container, `AppDetail` streams logs for that container.

**The correct mental model:**
- A **repo** contains one or more **stacks** (compose projects)
- A **stack** contains one or more **containers**
- The primary entity users care about is the **stack**, not the container
- Log streaming is fine at the container level, but navigation should be stack-first

## Task 1 — Fix the Data Hierarchy in `app.jsx`

Rewrite the data extraction in `app.jsx` so the primary list is **stacks**, not containers:

```jsx
// CORRECT: display stacks as the primary entities
const stacks = repos.flatMap(repo =>
  repo.stacks.map(stack => ({ ...stack, repoName: repo.name }))
);
```

Pass `stacks` to `AppGrid` (not containers). `AppGrid` shows one card per stack.

Each stack card should display:
- Stack name (directory basename)
- Repo name (smaller, secondary label)
- Composite status: `running` (all containers running), `degraded` (some stopped), `error`, `syncing`
- Last applied timestamp (`stack.lastApply`)
- Last commit SHA (first 7 chars) if available (`stack.lastSHA` — add this field to the API)
- List of containers with individual statuses (collapsed by default, expandable)

## Task 2 — Fix `AppGrid.jsx` to Show Stack Cards

Rewrite `AppGrid.jsx` to render **stack cards** (not container cards):

```jsx
// AppGrid.jsx — one card per stack
export function AppGrid({ stacks, onSelectStack }) {
  return (
    <div class="app-grid">
      {stacks.map(stack => (
        <StackCard
          key={`${stack.repoName}/${stack.name}`}
          stack={stack}
          onSelect={() => onSelectStack(stack)}
        />
      ))}
    </div>
  );
}

function StackCard({ stack, onSelect }) {
  const status = deriveStackStatus(stack.containers);
  return (
    <div class={`stack-card stack-card--${status}`} onClick={onSelect}>
      <div class="stack-card__name">{stack.name}</div>
      <div class="stack-card__repo">{stack.repoName}</div>
      <div class="stack-card__status">{status}</div>
      <div class="stack-card__meta">
        {stack.lastApply && <span>Applied {formatRelative(stack.lastApply)}</span>}
        {stack.lastSHA && <span class="stack-card__sha">{stack.lastSHA.slice(0, 7)}</span>}
      </div>
      <ContainerList containers={stack.containers} />
    </div>
  );
}
```

`deriveStackStatus(containers)`:
- All running → `"running"`
- Any stopped → `"degraded"`
- Stack status === "error" → `"error"`
- Otherwise → `"unknown"`

## Task 3 — Multi-Container Log Selection in `AppDetail.jsx`

When a user clicks a stack, `AppDetail` currently shows a single container's logs.
Update it to show a **container selector** so users can switch between containers:

```jsx
export function AppDetail({ stack, onClose }) {
  const [selectedContainer, setSelectedContainer] = useState(
    stack.containers[0]?.name ?? null
  );

  return (
    <div class="app-detail">
      <div class="app-detail__header">
        <h2>{stack.name}</h2>
        <button onClick={onClose}>✕</button>
      </div>

      {/* Container selector tabs */}
      <div class="app-detail__tabs">
        {stack.containers.map(c => (
          <button
            key={c.name}
            class={`tab ${c.name === selectedContainer ? 'tab--active' : ''}`}
            onClick={() => setSelectedContainer(c.name)}
          >
            {c.name}
            <span class={`tab__dot tab__dot--${c.status}`} />
          </button>
        ))}
      </div>

      {/* Log stream for selected container */}
      {selectedContainer && (
        <LogStream container={selectedContainer} />
      )}
    </div>
  );
}
```

Extract the log streaming logic from the current `AppDetail` into a separate
`LogStream` component that takes a `container` prop and starts/stops the `EventSource`
when the prop changes (use `useEffect` with `container` as dependency).

## Task 4 — Repo Status Panel

Add a small **Repos** sidebar or header panel showing repo sync status:

```jsx
function RepoStatus({ repos }) {
  return (
    <div class="repo-status">
      {repos.map(repo => (
        <div key={repo.name} class={`repo-status__item repo-status__item--${repo.status}`}>
          <span class="repo-status__name">{repo.name}</span>
          <span class="repo-status__sha">{repo.lastSHA?.slice(0, 7)}</span>
          <span class="repo-status__time">{formatRelative(repo.lastSync)}</span>
          <button class="repo-status__sync" onClick={() => triggerSync(repo.name)}>↻</button>
        </div>
      ))}
    </div>
  );
}
```

This replaces or augments the current sync button.

## Task 5 — Dark Mode

Add a dark mode toggle. Use CSS custom properties for all colors:

```css
/* index.css */
:root {
  --color-bg: #ffffff;
  --color-surface: #f5f5f5;
  --color-text: #1a1a1a;
  --color-text-secondary: #666;
  --color-border: #e0e0e0;
  --color-running: #22c55e;
  --color-degraded: #f59e0b;
  --color-error: #ef4444;
  --color-accent: #6366f1;
}

[data-theme="dark"] {
  --color-bg: #0f0f0f;
  --color-surface: #1a1a1a;
  --color-text: #f5f5f5;
  --color-text-secondary: #999;
  --color-border: #2a2a2a;
}
```

Toggle: button in the header, stores preference in `localStorage` as `stackd_theme`.
Apply `document.documentElement.setAttribute('data-theme', theme)` on load and toggle.

## Task 6 — Build Verification

After changes, verify the frontend builds and the binary embeds it correctly:

```bash
cd internal/ui
npm install
npm run build
cd ../..
go build ./...
```

The binary must start and serve the updated dashboard without errors.

## Acceptance Criteria

- Dashboard shows stacks (not containers) as primary entities in the grid
- Clicking a stack opens the detail panel with container tabs
- Each container tab streams that container's logs via SSE
- Repo sync status visible with per-repo sync trigger
- Dark mode toggle works and persists across page loads
- `npm run build` succeeds with no errors
- `go build ./...` succeeds with embedded new frontend
