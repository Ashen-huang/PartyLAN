# PartyLAN Light Cyber Neon Theme Implementation Plan

> Based on `2026-07-13-partylan-cyber-neon-theme-design.md`

## 1. Theme Tokens

Update `frontend/src/style.css` with semantic cold white-blue, ice-fog-blue, and saturated mint-cyan values. Add raised/muted surfaces, active borders, focus glow, and on-accent text tokens. Keep deep ocean text and coral errors for contrast.

## 2. Affected Surfaces

Update `frontend/src/App.css`, `frontend/src/components/LobbyView.css`, `frontend/src/components/SettingsPanel.css`, and `frontend/src/components/StatusBar.css`.

- Replace pure-white and deep-navy local fills with semantic surface/action tokens.
- Make the primary lobby action mint-cyan with a restrained glow.
- Give panel borders and status chrome a cool mint edge.
- Preserve the existing responsive hiding of secondary status metrics.
- Keep error and close-button danger states coral.

## 3. Verification

Run the frontend production build and existing Node tests. Start Vite with the existing Wails test stub, then inspect desktop, 375px, and low-height screenshots. Confirm the three target color families are visible across background, panels, buttons, and status bar, with no overlap or horizontal overflow.
