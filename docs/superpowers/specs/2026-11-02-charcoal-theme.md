# Charcoal theme — approved visual specification

User approval: “先按照这个来”. Reference images: `outputs/theme-reference-atmosphere/H-overview-particles-frosted.png` and `outputs/theme-reference-match/H-outbound-management.png` in the parent task workspace.

- Near-black canvas #171717; charcoal cards #262626, inset #2e2e2e; thin #3a3a3a edges, radius 14px, soft shadows. Primary blue #3b82f6; mint #69d6b2, yellow #f4c941, lavender #a58add as chart accents.
- Expanded 220px sidebar for new preferences, readable grouped navigation. Existing stored collapsed preference, mobile drawer, light/ultra-dark switching and every route remain available.
- Dense desktop bento layout, 16px gutters, 20px card padding. Small top title, sparse slow particles in background only, slight frosted sidebar. Opaque readable data cards; no neon or connected particle lines.
- Overview: actual total sent/received traffic, throughput history, connections, combined resource card, current external exits and runtime information. No fabricated monthly increase, protocol shares, inbound statistics or client versions.
- Outbounds: actual summary counts, compact external exit rows, native outbounds below, right inspector for existing exit edits; creation remains a dialog. Preserve password semantics, validation, template conflict protection, probe/log/delete and save failure behavior.
- Accessibility: decorative effects ignore input; stop animation while hidden, under reduced motion and outside normal dark theme. Clean up all animation/listener resources. Stack columns on mobile, preserve text and controls without viewport overflow.

Scope: frontend presentation only, no dependency, API, database, proxy execution or production changes. Existing staged external-outbound work and quarantined upstream install.sh are outside this theme patch.
