# PartyLAN Client Homepage Simplification Design

**Date:** 2026-07-11

## Goal

Simplify the desktop client's lobby so its primary actions are easy to scan and use in a small application window. Remove the current right-side information column, shorten explanatory copy, and move server configuration and technical details out of the main workflow.

## Scope

This change covers the lobby shown when the client is not in a room and the GameCast-related portion of the settings dialog. The in-room layout, sharing workflow, audio controls, voice controls, and legacy runtime lobby tools keep their existing behavior unless a shared scrolling rule applies to them.

## Lobby Layout

The lobby becomes a single-column layout with one centered content area whose maximum width is 720 pixels on desktop. On narrower windows it uses the available width with the existing page gutter.

The existing window title bar and status bar remain. Below them, the lobby contains:

1. A compact heading with `PartyLAN` as the title.
2. One short supporting line, equivalent to "Create a room or join a share in progress."
3. One unified GameCast action panel.

The current right-side welcome panel, quick-tip list, version block, network note, and lobby diagnostics panel are removed from the lobby. Diagnostics remain available from Settings.

The unified action panel uses sections and dividers instead of separate nested cards. Its default view contains only:

- Account access.
- The primary create-room action.
- The list of joinable rooms.

Server configuration is not rendered in the lobby. Recent rooms and detailed connection state are placed in a collapsed `More` section at the bottom of the action panel.

Legacy runtime modes continue to render their required room and node controls below the centered lobby content, but they must not restore the removed right-side information column.

## Account Section

When signed out, the account section shows username and password fields with Login and Register actions. Labels and placeholders remain concise. The two actions are visually secondary to Create Room.

When signed in, the form is replaced by one compact row containing the signed-in identity and a Logout action.

Authentication errors appear next to the account section. Password values are cleared after successful authentication and are never shown in status or diagnostic content.

## Room Actions

Create Room is the only primary button in the lobby. It remains disabled while an operation is running or while the configured server cannot be used.

Joinable rooms appear as a compact list below the primary action. Each row shows the room identifier, owner or origin when available, and a concise live/available state. Selecting a row joins that room directly.

The empty state is a single short sentence. The room list has a bounded height and its own vertical scrollbar when it contains more rows than fit comfortably.

## Secondary Information

The `More` disclosure is collapsed by default and remains in the user's current lobby session after manual expansion or collapse. It contains:

- Up to five recent rooms, including the existing remove-history action.
- Server, room, media, and signaling status values.

Technical detail is not repeated elsewhere in the lobby. If the server is missing, unreachable, or awaiting certificate trust, the default panel shows only a concise summary with an action that opens Settings. Full status and certificate information remain in Settings and the `More` disclosure where applicable.

## Settings Changes

Settings gains a GameCast Server section in GameCast client mode. The section contains:

- The server address field.
- A Check Server action.
- The current concise connection result.
- Certificate fingerprint and Trust Certificate action only when trust is required.

Saving Settings persists the normalized server address through the existing GameCast profile API. The lobby reads the saved profile and refreshes the room list against that server. Cancel closes the dialog without applying an unsaved server draft.

Server validation and certificate errors are displayed inside the GameCast Server section. Detailed runtime and media diagnostics continue to live in the existing Diagnostics section of Settings.

## Scrolling And Responsive Behavior

No content may be made unreadably small merely to fit the window.

- The scene content scrolls vertically when the centered lobby is taller than the available viewport.
- The joinable-room list uses a maximum height and an independent vertical scrollbar.
- The settings overlay remains fixed to the viewport.
- The settings header and action footer remain visible while only the settings body scrolls.
- The settings panel is constrained to the viewport height so its content cannot extend off-screen.
- At narrow widths, forms and action rows wrap into a single column without horizontal scrolling.
- Text wraps or truncates according to its role; truncated room identifiers expose the full value through both a tooltip and an accessible label, while instructions and errors wrap.

## Visual Direction

The existing light cyan and blue product palette remains, but the lobby no longer uses a dense two-column dashboard composition. The centered panel uses one surface, restrained borders, and clear internal spacing. Individual form groups are not rendered as nested cards.

The heading is compact rather than hero-sized. Supporting copy is limited to one line when space permits and wraps naturally in narrow windows. Create Room receives the strongest visual emphasis; login, registration, logout, settings, history removal, and disclosure actions use secondary or quiet treatments.

Interactive controls retain visible hover, focus, disabled, and busy states. Icon-only controls require an accessible label and stable hit area.

## State And Error Handling

Existing GameCast APIs and room state remain the source of truth. This redesign changes presentation and where the server draft is edited; it does not introduce a second server configuration store.

Expected states are:

- Signed out: account form, create-room action, room list.
- Signed in: compact identity row, create-room action, room list.
- No rooms: concise empty state.
- Busy: affected actions disabled with stable layout.
- Server not configured or unavailable: short lobby message with Open Settings action.
- Certificate trust required: short lobby message; fingerprint confirmation and trust action in Settings.
- Operation error: localized inline error near the affected section, with technical diagnostics available in Settings.

## Component Boundaries

Existing data fetching and GameCast handlers remain in `App.tsx`. Lobby presentation is extracted into a focused `LobbyView` component so the already-large render block does not grow further, without changing API ownership.

`SettingsPanel` receives the GameCast server draft, probe state, and callbacks needed to edit, check, trust, save, and cancel. The saved profile remains owned by the existing App state. Unsaved Settings state must be reset from the saved profile when the dialog is reopened.

## Verification

Automated verification must include the existing frontend build and any focused tests added for extracted state or rendering logic.

Visual verification must cover:

- Signed-out and signed-in lobby states.
- Empty, short, and long room lists.
- Expanded and collapsed secondary information.
- Missing server, unreachable server, and certificate trust states.
- A low-height desktop window where lobby and settings scrolling are both exercised.
- A narrow window where controls stack without overlap or horizontal overflow.

The final check must confirm that the former right-side content is absent, server address editing is available only in Settings, essential actions remain reachable, scrollbars appear when required, and no text or controls overlap.
