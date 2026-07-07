# Theme Config Findings And Plan

## Current Findings

- Spruce already has `[theme]` fields in `internal/config/config.go`.
- `ui.NewStyles(theme)` already maps those fields into Lip Gloss styles.
- The real bug is in `internal/app/model.go`: `New` starts with `styles := ui.DefaultStyles()` before it finishes building `actualCfg`.
- Because screens, palette, help, error banner, and player are built from `styles`, a loaded custom theme is effectively ignored.
- Everforest should remain the default. That default is good enough and should not be disrupted.
- `warning` exists in config but is not used by style construction.
- Palette selected row currently hardcodes Everforest foreground in `internal/ui/components/palette.go`, so even a valid configured theme can leak default colors.

## What Went Wrong In The Abandoned Attempt

- Theme presets created fake configurability instead of improving the default experience.
- Runtime theme switching touched too many models and increased risk.
- `surface` and UI size config changed visual behavior without a strong product need.
- Terminal font size cannot be controlled by this app. Mapping it to spacing is misleading and layout-hostile.
- Command palette appearance actions made the app feel configurable while adding little real value.

## Proper Minimal Plan

1. Keep config shape stable.
   - Keep existing `[theme]` color fields.
   - Do not add named presets.
   - Do not add `[appearance]`.
   - Do not add command palette actions for theme or size.

2. Fix Spruce theme loading.
   - Build `actualCfg` first.
   - Then call `styles := ui.NewStyles(actualCfg.Theme)`.
   - Pass those styles into login, library, playlists, queue, metadata editor, palette, help, error, and player.

3. Remove hardcoded palette foreground.
   - In `components.Palette.SetStyles`, do not force `#d3c6aa`.
   - Let `styles.Selected` decide both foreground and background.

4. Decide `warning`.
   - Preferred: add `Warning lipgloss.Style` to `ui.Styles` and use it only where warning UI exists.
   - If no warning UI exists, leave config field alone for compatibility but do not invent warning UI.

5. Add focused tests.
   - `app.New` with custom `Theme.Accent` produces model styles using that accent.
   - Palette selected style does not hardcode default foreground.
   - Existing default Everforest tests remain unchanged unless evidence says current default values are wrong.

## Non-Goals

- No runtime theme switching.
- No theme preset registry.
- No UI size or font size setting.
- No command palette appearance section.
- No broad visual redesign.

## Success Criteria

- Default Spruce looks the same as before.
- Custom `[theme]` in config works at startup.
- Tests prove config theme reaches app styles.
- Diff stays small and easy to review.
