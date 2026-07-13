# PartyLAN Light Cyber Neon Theme Design

**Date:** 2026-07-13

## Goal

Restore a coherent PartyLAN visual theme built from cold white blue, ice-fog blue, and saturated mint cyan. The interface remains light, but gains a clearer cyber-neon identity through stronger functional color, luminous edge treatment, and consistent semantic tokens.

## Scope

This change covers the shared application theme, lobby, status bar, and Settings surfaces. Components that already consume shared theme variables inherit the revised palette. The confirmed centered lobby structure, content hierarchy, scrolling behavior, room workflow, and application logic do not change.

## Visual Direction

The theme uses three dominant surface roles:

- Cold white blue for the application canvas and highest surfaces.
- Ice-fog blue for panels, inputs, status containers, and secondary controls.
- Saturated mint cyan for primary actions, focus, success, and active states.

Deep ocean blue remains the primary text color and provides contrast. Blue-gray supports secondary text. Coral red remains reserved for errors and destructive states.

Cyber-neon treatment is restrained and functional. Primary and active controls use a narrow mint-cyan border glow and a soft cyan shadow. The design does not add decorative light orbs, bokeh, large glow fields, or purple gradients. Text and controls must remain readable before visual effects are considered.

## Semantic Theme Tokens

`frontend/src/style.css` remains the source of truth. Existing variables are revised and additional surface/state variables are introduced where needed:

- `--bg-primary`: cold white-blue application canvas.
- `--bg-secondary`: ice-fog blue support layer.
- `--panel-bg`: translucent ice-blue panel surface.
- `--panel-header-bg`: slightly brighter cool-blue header surface.
- `--surface-raised`: high-emphasis translucent surface.
- `--surface-muted`: low-emphasis ice-blue surface.
- `--accent-bg`: saturated mint-cyan primary action.
- `--accent-hover`: slightly deeper mint-cyan hover state.
- `--accent-strong`: accessible teal used for labels and active text.
- `--accent-soft`: pale cyan support color.
- `--highlight`: focus and selected-state mint cyan.
- `--highlight-glow`: controlled cyan shadow used around active controls.
- `--border-color`: cool blue border.
- `--border-highlight`: mint-tinted active border.
- `--input-bg`: ice-blue input surface.
- `--hover-bg`: mint-tinted hover surface.
- `--text-primary`, `--text-secondary`, `--text-muted`: deep ocean blue through blue-gray text hierarchy.
- `--danger-bg`, `--danger-strong`: coral error treatment.
- `--panel-shadow`: cool blue depth shadow without gray or black heaviness.

Raw white, deep navy button fills, and duplicated mint/cyan RGBA values in the affected components are replaced by these semantic tokens. Component-specific values remain only where opacity or media presentation requires a local exception.

## Application Background And Chrome

The application background uses a cool white-blue to ice-fog-blue gradient with subtle cyan illumination at the top edges. Illumination stays continuous rather than appearing as separate decorative shapes.

The title bar uses a translucent ice-blue surface and a cool border. PartyLAN branding uses accessible teal. Window buttons stay quiet by default and receive a mint-tinted hover treatment; the close action retains coral hover feedback.

## Lobby

The centered action panel changes from an almost pure white card to a translucent ice-blue surface. Its border receives a subtle mint tint and a narrow cool glow. Internal dividers remain low contrast.

Create Room is the only strongly luminous action. It uses a saturated mint-cyan fill, deep ocean-blue text, and a controlled cyan shadow. Hover raises brightness and border contrast without changing control size.

Secondary buttons and input fields use ice-blue fills with cool borders. Hover and focus use mint-cyan borders and focus rings. Room rows use a pale cyan hover state. Live-room status uses accessible teal text plus its label; color is not the only signal.

The `More` section, empty state, recent rooms, and error presentation retain their existing structure while adopting shared surfaces and semantic colors.

## Status Bar

The status bar becomes an ice-fog-blue glass strip with a mint-tinted edge. Primary connection state remains readable in deep blue; connected and healthy values use accessible teal. Separators are cool blue and visually quiet.

The existing narrow-window behavior remains: secondary runtime metrics are hidden below 620 pixels so the status bar cannot overlap the lobby heading.

## Settings

The Settings overlay uses a cool translucent scrim. The Settings panel, header, scrollable body, footer, inputs, toggles, diagnostics, and server controls use the same semantic surface tokens as the lobby.

Save is a mint-cyan primary action. Cancel and Check Server use ice-blue secondary treatment. Focus rings, checked controls, and active states use the shared highlight token. Certificate and error content remain coral and do not borrow mint success styling.

## Accessibility And Interaction

- Normal text must meet a 4.5:1 contrast ratio against its rendered surface.
- Large text and meaningful control outlines must meet at least 3:1.
- Primary button text uses deep ocean blue rather than white if that produces stronger contrast on mint cyan.
- Focus rings remain visible at 2 to 3 pixels and are not replaced by glow alone.
- Hover, focus, pressed, selected, disabled, success, and error states remain distinguishable without relying only on color.
- Neon shadow does not change layout bounds or obscure neighboring content.
- Reduced-motion behavior remains unchanged; the theme adds no decorative animation.

## Verification

Automated verification includes the existing frontend production build, frontend tests, and scoped whitespace checks.

Visual verification covers:

- Signed-in lobby at desktop width matching the reported screenshot state.
- Signed-out lobby with enabled and disabled authentication actions.
- Empty and populated room lists.
- Expanded `More` section.
- Settings at desktop and narrow widths.
- A 375-pixel viewport and a low-height desktop viewport to confirm no regression in scrolling or overlap.
- Hover and keyboard focus states for the primary button, secondary button, room row, input, disclosure, and Settings controls.

The final review confirms that pure-white card dominance and deep-navy primary buttons are gone, the three target color families are visible across background, panel, buttons, and status bar, and no purple or dark-theme treatment has been introduced.
