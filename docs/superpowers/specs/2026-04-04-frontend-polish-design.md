# KumbulaCloud Frontend Polish — Design Spec

## Overview

Polish the dashboard UI with focus on the environment variables tab, plus light improvements across all tabs. No new Go handlers — CSS + template changes only, with minimal inline JS for the secret masking toggle.

**Stack:** Same as existing — Go `html/template`, htmx, Pico CSS, custom CSS.

---

## Environment Variables Tab

**Layout:** Replace flex-row layout with a proper table (Key, Value, Actions columns).

**System vars:** DATABASE_URL, PORT, APP_NAME, APP_URL shown at top in muted/disabled style with "System" label. Not editable or deletable.

**User vars:** Each row shows:
- Key: monospace, bold
- Value: masked (`••••••••••`) if key matches `/SECRET|KEY|TOKEN|PASSWORD|CREDENTIAL/i`, plain text otherwise. Eye icon toggles visibility via inline JS (value stored in `data-value` attribute).
- Actions: "Edit" and "Remove" buttons

**Inline editing:** Click "Edit" -> value cell becomes input, buttons become "Save" / "Cancel". Save sends `hx-post` to existing upsert endpoint. Cancel re-renders partial.

**Delete confirmation:** `hx-confirm="Remove KEY_NAME?"` on delete button.

**Add row:** Bottom of table, separated by divider. Key input (CSS `text-transform: uppercase`), value input, "Add" button.

**Feedback:** CSS flash animation (`.flash-row`) on changed rows after add/edit/delete.

---

## Overview Tab

**Status bar:** Horizontal strip — status badge (left), app URL link (center), redeploy button (right).

**Info cards:** 2-column grid (`.info-grid`) with: URL, Repository, Status, Last deployed.

**Git instructions:** Wrapped in collapsible `<details>` with summary "Git remote setup".

**Redeploy:** Moved to clear action section below cards. `hx-confirm` added.

---

## Build Log & Project Cards

**Build list:** Better styled collapsible logs, pulsing dot for active builds, readable duration format ("2m 34s").

**Project cards:** Small colored status dot (8px) + muted status text replaces large badge. Hover border effect.

**Dashboard empty state:** Centered card with prominent "Create your first project" button.

---

## Global

**Success feedback:** `.success-msg` div injected via `hx-swap-oob`, auto-fades after 3s (CSS animation).

**Settings tab:** Project info summary above danger zone. More spacing.

**Nav:** Subtle bottom border.

---

## CSS Classes

- `.success-msg` — green feedback bar with fade-out
- `.info-grid` — 2-column card grid for overview
- `.env-table` — table styling for env vars
- `.env-masked` — monospace dots
- `.env-system` — muted system var style
- `.pulse-dot` — animated dot for active builds
- `.status-dot` — small colored circle for cards
- `.flash-row` — highlight animation on changed rows
