# PartyLAN Client Homepage Simplification Implementation Plan

> Based on `2026-07-11-partylan-client-homepage-design.md`

## Goal

Replace the two-column lobby with one centered GameCast action panel, move server configuration into Settings, shorten lobby copy, and guarantee usable vertical scrolling in low-height and narrow windows.

## Boundaries

- Preserve the current GameCast API calls and room behavior.
- Preserve existing uncommitted work in `App.tsx`, `App.css`, `SettingsPanel`, and translations.
- Do not redesign the in-room screen.
- Keep legacy runtime room/node controls available below the simplified lobby.
- Do not add a new state store or frontend dependency.

## 1. Extract Lobby Presentation

Create `frontend/src/components/LobbyView.tsx` and `LobbyView.css`.

- Render the compact PartyLAN heading and one-line description.
- Render signed-out authentication fields or the signed-in account row.
- Render Create Room as the only primary lobby action.
- Render joinable rooms in a bounded, scrollable list.
- Render recent rooms, status values, and the web-room action inside a collapsed `More` disclosure.
- Render a concise server recovery message with an Open Settings action when configuration or trust blocks room actions.
- Pass existing callbacks and state from `App.tsx`; do not move network ownership into the component.

## 2. Replace The Existing Lobby Markup

Update `frontend/src/App.tsx`.

- Remove the hero panel, right-side welcome/diagnostics column, server field, and expanded status/history blocks from the lobby render path.
- Compute concise lobby status props and render `LobbyView`.
- Keep legacy `RoomControls` and `AvailableNodes` as optional children below the GameCast panel.
- Add explicit open/close Settings handlers that reset unsaved GameCast server drafts.

## 3. Move Server Configuration To Settings

Update `frontend/src/components/SettingsPanel.tsx` and `App.tsx`.

- Add a GameCast Server section only in GameCast client mode.
- Add a visible server label and address input.
- Add Check Server, concise probe result, conditional certificate fingerprint, and Trust Certificate controls.
- Make Check Server probe the draft without saving it.
- Save the normalized server address through `SaveGameCastProfile` from the Settings save flow.
- Restore the saved address when Settings is cancelled or reopened.
- Keep certificate trust persistence through the existing trust API.

## 4. Copy, Scrolling, And Accessibility

Update `frontend/src/i18n.ts`, `App.css`, `LobbyView.css`, and `SettingsPanel.css`.

- Replace the lobby hero copy with a short title/subtitle pair.
- Add labels for the new sections, More disclosure, Settings recovery, and server settings.
- Use a 720px centered maximum width and a 4/8px spacing rhythm.
- Ensure scene content scrolls vertically.
- Limit the room list height and enable vertical scrolling.
- Constrain Settings to the viewport; keep header/footer fixed and scroll only its body.
- Stack fields/actions on narrow windows and avoid horizontal overflow.
- Add `aria-expanded`, `aria-controls`, `role=alert`, visible focus states, full-value room labels, and accessible labels for icon-only actions.

## 5. Automated Verification

Run:

```powershell
Push-Location frontend
npm run build
node --test tests/*.test.mjs
Pop-Location
git diff --check
```

Resolve TypeScript, build, and whitespace errors without changing unrelated code.

## 6. Visual Verification

Start the Vite development server and use Playwright against the local page.

Verify at minimum:

- Desktop viewport around 1280x800.
- Narrow viewport around 520x760.
- Low-height desktop viewport around 1000x560.
- Main lobby has no right-side welcome or diagnostic column.
- Server address appears only in Settings.
- More disclosure expands without layout overlap.
- Lobby, room list, and Settings body scroll when constrained.
- Inputs, buttons, labels, focus states, errors, and disabled states remain readable.
- Browser console contains no new errors caused by the UI change.

## Completion Criteria

- The lobby matches the confirmed single-panel information hierarchy.
- Server edit/check/trust actions work from Settings and are absent from the lobby.
- The old right-side content is removed.
- Long content remains reachable through scrollbars at supported window sizes.
- Frontend build, existing tests, and visual checks pass.
