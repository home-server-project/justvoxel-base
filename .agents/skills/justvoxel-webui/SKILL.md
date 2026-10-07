---
name: justvoxel-webui
description: Change or review the JustVoxel WebUI, including Go handlers, templates, JavaScript, CSS, navigation, workspaces, windows, roles, forms, dialogs, progress, and responsive desktop/mobile behavior.
---

# JustVoxel WebUI

Treat the WebUI as the JustVoxel appliance control interface, not a generic Linux administration panel.

## Core Rules

- Show JustVoxel and Minecraft concepts instead of systemd, Podman, mount, or API jargon wherever practical.
- Keep the frontend unprivileged. Route privileged changes through supported Management Agent operations.
- Never add arbitrary shell, mjust, systemd, or Podman execution to the WebUI.
- Keep role visibility and backend authorization aligned. Hiding a control is not authorization.
- Do not place secrets in localStorage, query strings, logs, or unnecessary JavaScript state.
- Use browser storage only for harmless UI preferences/layout state.
- Reuse existing JustVoxel workspaces, components, patterns, and styling before inventing new UI machinery.
- Do not create a new top-level workspace or oversized control simply because it is the easiest implementation.
- Reuse existing destructive confirmation patterns instead of adding warning chains.
- Long operations must expose understandable state/progress. Technical console output may be optional transparency.

## Responsive Behavior

Desktop and mobile are related but must both remain usable.

Desktop:

- Prefer fitting important workspace content cleanly without vertical scrolling when reasonably possible.
- Treat desktop scrolling as an exception, not the default layout strategy.

Mobile and small tablets:

- Scrolling is acceptable when the content cannot fit.
- Every action must remain reachable.
- Do not leave controls clipped below an unscrollable region or create navigation dead ends.

When fixing one layout class, explicitly check that the other class does not regress.

## Verification

Run relevant WebUI Go/JavaScript/source checks during development.

Source checks can establish technical completeness, not visual or runtime acceptance. Final visual/runtime acceptance happens in the human development VM.
