# Release Checklist

Every release follows these steps in order. The version lives in three places —
the CLI constant, the plugin manifest and the changelog — and **step 2** keeps all
three aligned with a single command.

## 1. Prepare

- [ ] Write the changelog prose for the new version. The bump command creates the
      `## [X.Y.Z] — YYYY-MM-DD` heading if it is missing, but the prose is yours.
- [ ] Confirm the working tree is clean: `git status --short`.

## 2. Bump the version — one command

```sh
make bump-version VERSION=1.0.6
```

This rewrites, in one pass:

| File | Field | Read by |
| --- | --- | --- |
| `internal/fleet/catalog.go` | `Version` constant | `mcode-router --version` |
| `plugins/mcode-router/.minimax-plugin/plugin.json` | `version` | the packaged plugin |
| `CHANGELOG.md` | release heading + link reference | the release notes |

The command refuses a version that is not `major.minor.patch`, and re-reads every
file afterwards, so a half-applied bump fails loudly instead of shipping. It is
idempotent: running it twice for the same version changes nothing the second time.

## 3. Verify the version is aligned

```sh
make release-check
```

Prints the version found in each file and exits non-zero when they disagree. The
`make dogfood` gate cross-checks `--version` against the plugin manifest as well.

## 4. Run the gates

```sh
make all
```

## 5. Commit, tag and publish

```sh
git add -A
git commit -m "release: v1.0.6 — <summary>"
git tag -a v1.0.6 -m "v1.0.6 — <summary>"
git push origin main
git push origin v1.0.6
```

Pushing the tag runs `.github/workflows/release.yml`, which cross-compiles the
static binaries, writes `SHA256SUMS` and publishes the GitHub release. Verify:

```sh
gh run list --limit 3
gh release view v1.0.5
```

> **Note.** Push the tag *after* the version bump is committed. The workflow builds
> from the tagged commit, so a tag created before the bump ships a binary whose
> `--version` does not match the tag.

## 6. Refresh the local install

```sh
make build
./install.sh
```

## 7. Announce

- [ ] Release notes are live at
      `https://github.com/UberMetroid/mcode-router/releases/tag/vX.Y.Z`.
