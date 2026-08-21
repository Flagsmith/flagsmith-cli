# Command reference site

The [Hugo](https://gohugo.io) site published to <https://flagsmith.github.io/flagsmith-cli/> on every release.

All of `content/` is generated from the CLI's own help text by [`cmd/docgen`](../cmd/docgen) and is gitignored.

## Preview locally

Requires [Hugo extended](https://gohugo.io/installation/) (`brew install hugo`)
and Go.

```sh
go run ./cmd/docgen -out website/content   # from the repository root
cd website && hugo server
```

Then open <http://localhost:1313>. Re-run `docgen` after changing help text.

## What is hand-written

| Path                                   | Purpose                            |
| -------------------------------------- | ---------------------------------- |
| `hugo.yaml`                            | Site config, navigation, theme     |
| `layouts/_partials/navbar-title.html`  | Theme override: version badge      |
| `layouts/_partials/custom/footer.html` | Names the release being documented |
| `layouts/home.html`                    | Theme override                     |
