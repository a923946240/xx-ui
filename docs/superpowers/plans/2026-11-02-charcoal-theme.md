# Charcoal theme implementation plan

Spec: ../specs/2026-11-02-charcoal-theme.md

1. Theme foundation: shared AntD tokens, scoped shell/cards/sidebar, bounded background canvas. Test animation cleanup/reduced motion/visibility before implementation; retain light and ultra-dark modes.
2. Outbound workspace: list and summary composition plus inline inspector. First change tests to assert inline editing, failed-save retention and pending-template protection; add a polling regression test. Reuse existing form/API/schema.
3. Overview bento: compose real status/history/external exit data, reuse existing charts and runtime details. Preserve all management actions and privacy controls. Add a smoke test for source-derived traffic/exit states where useful.
4. Verify typecheck, lint, formatting, relevant component tests, full frontend suite/build; build a local preview binary and inspect desktop/mobile in browser. Compare actual screenshots against approved images. One final fresh review, fix actionable findings and repeat affected checks.

Continue the current branch/checkout because external-exit work is in progress and already staged. No commits, pushes or deployment requested. Keep theme changes distinguishable from the index. Use the existing synthetic local fixture for preview only.
