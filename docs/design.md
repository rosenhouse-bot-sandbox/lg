# Design

These decisions are ones that code and specs cannot show.
`internal/testsupport/recordings/github_test.go` pins the GitHub API behavior that lg depends on.

## Scope

- Coding agents are the primary users, and people are secondary.
- Each developer runs their own daemon with their own `gh auth` token and their own store.
- lg mirrors one repository.
- A self-hosted GitHub Enterprise Server is the main production target. github.com is the development target.
- lg aims to have a run attempt on disk 5 to 15 minutes after it completes. It never mirrors logs of jobs still in progress.
- lg is sized for under about 100 runs a day and a few GB per 90 days, so `rg` over the raw tree is fast enough.
- The store is a cache, not a system of record. Everything under `data/` came from GitHub or is derived from what did.

## Non-goals

- lg does not normalize logs.
- lg does not parse test results.
- lg has no MCP server.

## Open questions

- No recording shows an expired log. lg treats a log's 410 as `expired`, as GitHub's docs say.
- No recording shows a listed artifact with `expired: true`. lg tombstones one as `expired` without a request, as GitHub's docs say.
- What `head_branch` holds for a tag push is unverified.
