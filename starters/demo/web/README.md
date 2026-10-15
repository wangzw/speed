# starters/demo/web

The frontend application of this skeleton. It is a placeholder: this round
builds the Go program and leaves the frontend to the round that lands it.

## What is here now

`dist/` holds a single placeholder `index.html`, and it is **tracked in git on
purpose**: the repository ignores `dist/` as a build output, but `go:embed`
refuses to build when the directory is absent, so a checkout has to carry one.
Git will not re-include a file under an ignored directory, which is why the file
is committed with `git add -f` rather than exempted in a `.gitignore` — the next
change to it needs the same.

The Go program embeds
`web/dist` with `go:embed` and serves it through `pkg/webui` on the `web`
endpoint, so `serve` boots with a page to answer before a real frontend exists.
The embed is what fixes the build order — the frontend is built first and the Go
binary second — and that order does not change when the real bundle arrives.

## What lands here

The application source, its build, and its `package.json`. It declares its
dependency on the published `@speed/*` packages by registry version range, the
way a copied-out product does, and it is built into `dist/` before the Go
binary is built. The frontend lives here and not in the repository's top-level
`web/`: a delivered consumer project's frontend sits with the project.

None of that exists yet. It waits on the route package and the published
`@speed/*` packages, neither of which is available in this repository today.
