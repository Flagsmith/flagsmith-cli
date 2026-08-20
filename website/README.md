# Command reference site

The [Hugo](https://gohugo.io) site published to
<https://flagsmith.github.io/flagsmith-cli/> by
[`.github/workflows/docs.yml`](../.github/workflows/docs.yml) on every release
tag.

The site *is* the command reference: all of `content/` is generated from the
CLI's own help text by [`cmd/docgen`](../cmd/docgen) and is gitignored. Nothing
about a command is written here by hand — fix the `Short`, `Long` or `Example`
on the command itself in `internal/cmd/` and the page follows. There is
deliberately no landing page: installing and getting started are documented in
the [README](../README.md#install) and on
[docs.flagsmith.com](https://docs.flagsmith.com/integrating-with-flagsmith/CLI),
and a third copy here would be the one that goes stale.

The command tree becomes a directory tree, so `flagsmith flag update` is served
at `/flag/update/`, the `flagsmith` root command is the home page, and the
sidebar nests the way the CLI does.

## Preview locally

Requires [Hugo extended](https://gohugo.io/installation/) (`brew install hugo`)
and Go.

```sh
go run ./cmd/docgen -out website/content   # from the repository root
cd website && hugo server
```

Then open <http://localhost:1313>. Re-run `docgen` after changing help text.

## What is hand-written

| Path | Purpose |
| --- | --- |
| `hugo.yaml` | Site config, navigation, theme |
| `layouts/_partials/navbar-title.html` | Theme override: version badge |
| `layouts/_partials/custom/footer.html` | Names the release being documented |
| `layouts/home.html` | Theme override, see below |

Only the newest release is published: Pages serves a single artifact, so there
are no `/vX.Y.Z/` URLs. The version is therefore shown twice — as a badge beside
the navbar title, where it is seen without scrolling, and once in the footer with
the context that explains it. Both read `params.version` in `hugo.yaml`, which
release-please bumps in its release pull request (`website/hugo.yaml` is one of
its `extra-files`), so a plain `hugo server` shows exactly what a deploy would.

Hextra's home layout hides the sidebar, which would leave the front page — the
root command's own page — with no navigation at all. `layouts/home.html` is the
theme's ordinary reference-page layout, copied so the home page gets the command
tree too. Re-check it against the theme when bumping Hextra.

`go.mod` here is a Hugo module file and is intentionally separate from the CLI's
own `go.mod`, so the theme never becomes a dependency of the released binary.
