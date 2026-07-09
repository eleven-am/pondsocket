# JavaScript releases

The JavaScript packages are npm workspaces with one lockfile at `javascript/package-lock.json`. Packages are built and published from their workspace roots; `files: ["dist"]` controls the tarball contents.

## Prepare a release

1. Update the version in every changed package's `package.json`.
2. When Common or Core changes, update dependent PondSocket version ranges in the other package manifests.
3. Run `npm ci` and `npm run validate` from `javascript/`.
4. Commit the manifests and lockfile, merge them to the release branch, then publish a GitHub release from that commit with an `npm/`-prefixed tag, such as `npm/2026-07-09.1`.

The `npm-publish.yml` workflow runs automatically only for `npm/`-prefixed releases, so releases for the Go, Python, and Rust packages cannot publish pending npm versions. It can also be started manually. The workflow validates the repository again and publishes only package versions that do not already exist. Publication order is Common, Core, Client, Express, then Nest.

Run `npm run release -- --dry-run` to list the versions that the workflow would publish without changing the registry.

## npm trusted publishing

Configure a trusted publisher for each `@eleven-am/pondsocket*` package on npm with:

- Organization: `eleven-am`
- Repository: `pondsocket`
- Workflow filename: `npm-publish.yml`
- Allowed action: `npm publish`

The workflow uses GitHub OIDC, so it does not require a long-lived `NPM_TOKEN`. Keep the repository URL in each package manifest synchronized with the GitHub repository because npm validates it during trusted publication.

For an exceptional local release, authenticate with npm and run `npm run release -- --allow-local`. CI publishing is preferred because it produces npm provenance and runs from a clean checkout.
