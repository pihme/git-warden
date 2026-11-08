# gh-pages source

This branch holds the static GitHub Pages site for `pihme/git-warden`.

Generated on 2026-11-08 from `main` at commit `c0c77da9d51310b36f4266e8e7d6a676ea3d0714`.
Self-contained `index.html`, `chronik/index.html` (Chronicles) `jigsaw/index.html` (family page, from `family.json`) `handbook/` (guard handbooks rendered from the repo's `docs/*.md` with `mdlite.py`) and, where present, `namesake/index.html` (from `namesake-<repo>.json`), all with inline CSS, no build step, no trackers; plus favicons (`favicon.svg`, `favicon.png`, `favicon.ico`, `apple-touch-icon.png`) and `.nojekyll`.
Generator: `gen.py` (layout B, sidebar handbook) + per-site content script + `chronik-<repo>.json` chronicle data + `family.json` project-family registry + `status-<repo>.json` (Current status; open issues and releases pulled live via `gh` at build time).
Regenerate when `main` changes; do not merge this branch into `main`.
