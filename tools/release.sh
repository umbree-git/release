#!/usr/bin/env bash
# release.sh — PUBLISH an already-staged umbree|umbreed release.
#
# Usage:
#   bash tools/release.sh --distribute-only <umbree|umbreed> <stamp> [--dry-run]   # stable publish
#   bash tools/release.sh --channel beta     <umbree|umbreed> <stamp> [--dry-run]   # beta publish
#
# This repo has NO shell build path. Building, signing, and (with --apple)
# notarizing the artifact set live entirely in `rkit build` (the produce half),
# which stamps + cross-compiles the component for darwin/{arm64,amd64} +
# linux/{arm64,amd64}, writes SHA256SUMS.txt, and minisign-signs it into
# dist/<stamp>/.
#
# Both verbs publish THAT already-staged dist/<stamp>/ WITHOUT building,
# signing, notarizing, or bumping a version — they run only the publish half.
# They share the pre-flight (staged dir, module gate, cut-origin guard, the
# release key check) and differ in where the bytes go:
#
# --distribute-only (STABLE):
#   1. git-tags <comp>/<stamp> + publishes a GitHub Release on umbree-git/release.
#   2. mirrors the artifacts to the R2 download mirror and rewrites its catalog,
#      when the mirror is configured — skipped, loudly, when it is not.
#   3. records versions/<comp>.stamp (the version floor the bootstrap bakes),
#      regenerates the outer bootstrap + version JSONP and scp's the static
#      surface (install.sh, version.js, umbree-release.pub, site/index.html)
#      to the release host.
#   4. records a [RELEASED: <comp>] marker commit.
#   GitHub Releases host the zips and stay primary; the R2 mirror is a fallback
#   and the source of the published-stamp catalog.
#
# --channel beta (BETA — beta.md; "private" = R2-only, no GitHub Release):
#   1. asserts versions/<comp>.beta sorts above versions/<comp>, and that the
#      stamp is beta-shaped (v<X.Y.Z>.beta.<date>.<sha8>).
#   2. REQUIRES the R2 mirror: a beta cut creates no GitHub Release, so an
#      unconfigured mirror is a refusal (exit 1, before any write), not a skip.
#   3. git-tags <comp>/<stamp> and pushes the tag (history; what
#      prune-releases.sh --channel beta counts) — no Release is created, so the
#      stable bootstrap's /releases resolution never sees it.
#   4. uploads every artifact to R2 <comp>/beta/<stamp>/<file>, then
#      <comp>/beta/latest.json LAST.
#   5. records versions/<comp>.beta.stamp, renders <comp>/beta.install.sh and
#      <comp>/beta.version.js (the twins feature 02's `umbree update` fetches —
#      their names and URLs never move), and scp's ONLY those two to the
#      release host. install.sh / version.js / the pubkey / site are the stable
#      surface and are not touched.
#   6. records a [RELEASED: <comp> beta] … (private) marker commit.
#
# There is no console/dispatcher. Opening, approving and closing a cycle are
# operator steps (tools/version.sh --seed; tools/adopt-beta-version.sh;
# README "Beta channel") — nothing here does them.
#
# On --dry-run: validates the staged dir + component, runs the gates in
# report mode, then prints "would: ..." for every publish action and returns —
# no GitHub/git/ssh/scp/network writes.
#
# Env:
#   RELEASE_HOST           ssh alias for the nginx static host — REQUIRED (no default --
#                           this repo is public, so a default would ship the production
#                           hostname)
#   STATIC_DIR             absolute static dir on that host — REQUIRED (no default,
#                           same reason)
#   UMBREE_SRC_UMBREE      umbree component source worktree — REQUIRED (no default) when
#                           distributing umbree. The cut-origin guard requires it to BE
#                           the registry main folder (<brand>/cli/code/main) on stable, or
#                           its code/beta sibling worktree on beta; a different tree is
#                           permitted only under --dry-run
#   UMBREE_SRC_UMBREED     umbreed component source worktree — REQUIRED (no default) when
#                           distributing umbreed; same rule (<brand>/daemon/code/{main,beta})
#   BETA_BRANCH            beta only — the branch the beta worktree must be on
#                           (default: config/beta-branch, else "beta"; beta.md §2)
#   UMBREE_RELEASE_REPO    GitHub repo for releases (default umbree-git/release)
#   UMBREE_GH              GitHub CLI to publish with (default `gh`) — set it when
#                           your environment provides a different one
#   UMBREE_R2_ACCOUNT      Cloudflare account id for the download mirror. Unset =
#                           stable skips the mirror (GitHub remains the only channel);
#                           beta REFUSES
#   UMBREE_R2_CREDS        path to the R2 S3 credentials TOML. Unset = same
#   UMBREE_R2_BUCKET       mirror bucket (default umbree-downloads)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# shellcheck source=tools/module_gate.sh
source "${REPO_ROOT}/tools/module_gate.sh"
# shellcheck source=tools/release_origin.sh
source "${REPO_ROOT}/tools/release_origin.sh"

usage() { echo "✗ usage: release.sh --distribute-only <umbree|umbreed> <stamp> [--dry-run]   (stable)
         release.sh --channel beta     <umbree|umbreed> <stamp> [--dry-run]   (beta)" >&2; }
print_help() { awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0"; }

# Two entry verbs, each taking its component + stamp positionally right after
# the verb. --distribute-only is the GitHub-Release path and is therefore a
# stable-channel verb; --channel here takes exactly `beta`, because the stable
# publish IS --distribute-only and a second spelling of it would be a second
# thing to drift.
CHANNEL=stable; VERB=""; DIST_COMP=""; DIST_STAMP=""
case "${1:-}" in
    --distribute-only)
        VERB=distribute; shift
        DIST_COMP="${1:-}"; DIST_STAMP="${2:-}"
        [ -n "${DIST_COMP}" ] && [ -n "${DIST_STAMP}" ] || { usage; exit 2; }
        shift 2 ;;
    --channel)
        case "${2:-}" in
            beta) ;;
            stable) echo "✗ --channel stable is not a verb: the stable publish is --distribute-only <comp> <stamp>" >&2; exit 2 ;;
            *) echo "✗ --channel takes beta here (got '${2:-}'); the stable publish is --distribute-only" >&2; exit 2 ;;
        esac
        VERB=beta; CHANNEL=beta; shift 2
        DIST_COMP="${1:-}"; DIST_STAMP="${2:-}"
        [ -n "${DIST_COMP}" ] && [ -n "${DIST_STAMP}" ] || { usage; exit 2; }
        shift 2 ;;
    -h|--help) print_help; exit 0 ;;
    *) usage; exit 2 ;;
esac

# ---- args: both verbs accept ONLY --dry-run --------------------------------
# Publishing an already-staged dist/ takes no build/sign/notarize/bump flags —
# those already ran in `rkit build`. Accepting them would silently set unused
# vars and imply behavior that never happens under this mode.
DRY_RUN=0
for arg in "$@"; do
    case "${arg}" in
        --dry-run) DRY_RUN=1 ;;
        -h|--help) print_help; exit 0 ;;
        --channel|--channel=*)
            if [ "${VERB}" = distribute ]; then
                echo "✗ --distribute-only is a stable-channel verb; a beta cut publishes to R2 with: release.sh --channel beta <comp> <stamp>" >&2
            else
                echo "✗ --channel is given once, as the verb" >&2
            fi
            exit 2 ;;
        *) echo "✗ ${VERB} accepts only --dry-run (got '${arg}')" >&2; exit 2 ;;
    esac
done

# ---- config / defaults ------------------------------------------------------
# RELEASE_HOST / STATIC_DIR: no default. This repo is public, so a default would
# have to be the production hostname and static path. Supply both from your operator configuration.
RELEASE_HOST="${RELEASE_HOST:?set RELEASE_HOST to the ssh alias for the nginx static host}"
STATIC_DIR="${STATIC_DIR:?set STATIC_DIR to the absolute static dir on that host}"
RELEASE_REPO="${UMBREE_RELEASE_REPO:-umbree-git/release}"

# component source worktrees. No default: this repo is public, so a default
# would have to be one operator's absolute path. Only the worktree for the
# component actually being distributed is required — resolved lazily inside
# src_for(), not unconditionally up front, so distributing umbreed never
# demands UMBREE_SRC_UMBREE (and vice versa).
src_for() {
    case "$1" in
        umbree)
            : "${UMBREE_SRC_UMBREE:?set UMBREE_SRC_UMBREE to the component source worktree (the cli checkout that ships cmd/umbree)}"
            printf '%s' "${UMBREE_SRC_UMBREE}"
            ;;
        umbreed)
            : "${UMBREE_SRC_UMBREED:?set UMBREE_SRC_UMBREED to the component source worktree (the daemon checkout that ships cmd/umbreed)}"
            printf '%s' "${UMBREE_SRC_UMBREED}"
            ;;
    esac
}

# registry_main_for <comp> — the registry main folder for a component, derived
# from the committed workspace layout exactly as tools/release.command derives
# UMBREE_SRC_*: this repo is <brand>/release/code/main, the component is a
# sibling brand repo. Never an absolute path in a public file. Printed
# unresolved when the folder does not exist, so a refusal still names it.
registry_main_for() {
    local brand_root
    brand_root="$(cd "${REPO_ROOT}/../../.." 2>/dev/null && pwd)" || brand_root="${REPO_ROOT}/../../.."
    case "$1" in
        umbree)  printf '%s/cli/code/main' "${brand_root}" ;;
        umbreed) printf '%s/daemon/code/main' "${brand_root}" ;;
    esac
}

# assert_origins <comp> <channel> <mode> — the cut-origin guard over the
# component source AND this repo (tools/release_origin.sh). Also run by
# tools/release.command before `rkit build`; re-asserted here because this
# script is also run by hand, and `rkit build` has staged the version bump by
# the time it runs — which is exactly the staged tolerance the release repo
# gets (versions/<comp>[.beta] + its stamp, nothing else).
assert_origins() {
    local comp="$1" channel="$2" mode="$3" reg src
    reg="$(registry_main_for "${comp}")"; src="$(src_for "${comp}")"
    if [ "${channel}" = beta ]; then
        assert_release_origin "${comp}" "${src}" "${reg}" "${mode}" beta || exit 1
    else
        assert_release_origin "${comp}" "${src}" "${reg}" "${mode}" || exit 1
    fi
    local -a staged=()
    while IFS= read -r p; do [ -n "$p" ] && staged+=("$p"); done <<EOF
$(staged_tolerance_for 1 "${comp}" "${channel}")
EOF
    assert_release_origin "release repo" "${REPO_ROOT}" "${REPO_ROOT}" "${mode}" ${staged[@]+"${staged[@]}"} || exit 1
}

# The GitHub CLI to publish with. Defaults to `gh`; set UMBREE_GH when your
# environment provides a different one (for example a wrapper that selects an
# account per repository). This file names no tool beyond the default.
GH_CLI="${UMBREE_GH:-gh}"

# r2_configured — both the account and a readable credentials file.
r2_configured() {
    [ -n "${UMBREE_R2_ACCOUNT:-}" ] && [ -n "${UMBREE_R2_CREDS:-}" ] && [ -f "${UMBREE_R2_CREDS}" ]
}

# require_r2 — beta is R2-only: an unconfigured mirror is a refusal, not a
# skip. Runs BEFORE the tag is created so a refused beta leaves no trace.
require_r2() {
    r2_configured || {
        echo "✗ beta is R2-only — set UMBREE_R2_ACCOUNT and UMBREE_R2_CREDS (a beta cut publishes no GitHub Release, so there is nothing else to serve it from)" >&2
        exit 1
    }
}

# ---- mirror_r2 <comp> <stamp> <stage> [channel] -----------------------------
# Uploads the staged artifacts to the R2 download mirror and rewrites the
# channel's catalog: <comp>/latest.json on stable (the catalog
# gen-version-jsonp.sh reads for the published stamp), <comp>/beta/latest.json
# on beta, with the artifacts under <comp>/[beta/]<stamp>/. The manifest is
# uploaded LAST (tools/r2-mirror), so a reader never sees a catalog that names
# bytes not yet there.
#
# Config. All of it is the operator's; this repo names no account, no
# credential and no path on anyone's machine:
#
#   UMBREE_R2_ACCOUNT   Cloudflare account id — REQUIRED to mirror
#   UMBREE_R2_CREDS     path to the R2 S3 credentials TOML — REQUIRED to mirror
#   UMBREE_R2_BUCKET    bucket name (default: umbree-downloads — the bucket is
#                       public by design, so naming it here leaks nothing)
#
# STABLE: UNCONFIGURED IS A SKIP, NOT A FAILURE. GitHub is where the binaries
# actually come from, so a cut without the mirror says so and carries on.
# BETA: unconfigured never reaches here — require_r2 refused first.
#
# CONFIGURED BUT FAILING IS A STOP. Once an operator has said "mirror this",
# a silent half-publish is the bad outcome: the GitHub release exists, the
# catalog still advertises the PREVIOUS stamp, and version.js would be
# regenerated from that stale catalog — publishing a page that names an older
# release than the one just cut. Clawee learned this on 2026-08-20; the same
# reasoning applies here, and the recovery is spelled out rather than left to
# be rediscovered mid-incident.
mirror_r2() {
    local comp="$1" stamp="$2" stage="$3" channel="${4:-stable}"
    local account bucket creds semver
    account="${UMBREE_R2_ACCOUNT:-}"
    creds="${UMBREE_R2_CREDS:-}"
    bucket="${UMBREE_R2_BUCKET:-umbree-downloads}"
    if [ "${channel}" = beta ]; then
        semver="$(cat "${REPO_ROOT}/versions/${comp}.beta")"
    else
        semver="$(cat "${REPO_ROOT}/versions/${comp}")"
    fi

    if [ -z "${account}" ] || [ -z "${creds}" ]; then
        echo "⚠ R2 mirror skipped: UMBREE_R2_ACCOUNT/UMBREE_R2_CREDS not set — GitHub Releases are published and remain primary" >&2
        return 0
    fi
    if [ ! -f "${creds}" ]; then
        echo "⚠ R2 mirror skipped: credentials file not found — GitHub Releases are published and remain primary" >&2
        return 0
    fi

    echo "→ mirroring ${comp} ${stamp} (${channel}) → R2 bucket ${bucket}" >&2
    if ( cd "${REPO_ROOT}/tools/r2-mirror" && "${GO_BIN:-go}" run . \
            --account "${account}" --bucket "${bucket}" \
            --stage-dir "${stage}" --comp "${comp}" --channel "${channel}" \
            --version "${semver}" --stamp "${stamp}" \
            --creds "${creds}" >&2 ); then
        echo "✓ mirrored ${comp} (catalog updated)" >&2
        return 0
    fi

    echo "✗ R2 mirror FAILED for ${comp} ${stamp} — stopping the ${channel} publish here." >&2
    if [ "${channel}" = beta ]; then
        echo "  State: the tag ${comp}/${stamp} IS pushed; the beta catalog did NOT update;" >&2
        echo "  beta.install.sh / beta.version.js, the scp to the release host and the" >&2
        echo "  [RELEASED: ${comp} beta] marker commit did NOT run. No GitHub Release exists" >&2
        echo "  (a beta never creates one)." >&2
        echo "  Recover by hand — re-run the mirror with the arguments above, then" >&2
        echo "  tools/gen-bootstraps.sh, tools/gen-version-jsonp.sh --channel beta ${comp}," >&2
        echo "  the scp of the two beta twins, and the marker commit." >&2
    else
        echo "  State: the GitHub release IS published; the catalog did NOT update;" >&2
        echo "  version.js, the bootstraps, the scp to the release host and the" >&2
        echo "  [RELEASED] marker commit did NOT run." >&2
        echo "  This cannot simply be re-run: --distribute-only refuses a tag it has" >&2
        echo "  already created, and the GitHub release for that tag now exists." >&2
        echo "  Recover by hand — re-run the mirror with the arguments above, then" >&2
        echo "  tools/gen-bootstraps.sh, tools/gen-version-jsonp.sh ${comp}, the scp," >&2
        echo "  and the marker commit." >&2
    fi
    exit 1
}

# ---- shared pre-flight -------------------------------------------------------
# validate_stage <comp> <stamp> — the staged dir rkit build produced.
validate_stage() {
    local comp="$1" stamp="$2" stage="${REPO_ROOT}/dist/$2"
    case "${comp}" in
        umbree|umbreed) ;;
        *) echo "✗ unknown component: ${comp}" >&2; exit 1 ;;
    esac
    [ -d "${stage}" ] || { echo "✗ staged dir missing: ${stage} (run rkit build first)" >&2; exit 1; }
    for f in SHA256SUMS.txt SHA256SUMS.txt.minisig; do
        [ -f "${stage}/${f}" ] || { echo "✗ missing ${f} in ${stage} (rkit build must produce it)" >&2; exit 1; }
    done
    compgen -G "${stage}/${comp}-*.zip" >/dev/null \
        || { echo "✗ no ${comp}-*.zip found in ${stage} (rkit build must produce it)" >&2; exit 1; }
}

# verify_release_key <stage> — hard gate: the staged SHA256SUMS.txt MUST verify
# against the SHIPPED public key before anything is published. `rkit build
# --dry-run` signs with a TEST key yet produces a byte-identical dist/<stamp>/,
# so without this an operator could publish TEST-signed sums that every
# end-user install.sh (real key) rejects — a silent dead release.
verify_release_key() {
    command -v minisign >/dev/null 2>&1 || { echo "✗ required tool not found: minisign" >&2; exit 1; }
    minisign -V -p "${REPO_ROOT}/umbree-release.pub" \
        -m "$1/SHA256SUMS.txt" -x "$1/SHA256SUMS.txt.minisig" >/dev/null 2>&1 \
        || { echo "✗ staged dist is not signed by the release key — re-run 'rkit build --apple --sign-key <real key>'" >&2; exit 1; }
}

# require_release_host — upfront reachability check; without it a down host
# fails fast only at the scp below, AFTER the tag (and, on stable, the GitHub
# Release) already published.
require_release_host() {
    ssh -o BatchMode=yes -o ConnectTimeout=5 "${RELEASE_HOST}" 'true' 2>/dev/null \
        || { echo "✗ cannot ssh to ${RELEASE_HOST}" >&2; exit 1; }
}

# create_tag <comp> <stamp> — the local annotated tag; refused if it exists.
create_tag() {
    local tag="$1/$2"
    if git rev-parse "refs/tags/${tag}" >/dev/null 2>&1; then
        echo "✗ tag ${tag} already exists locally" >&2
        exit 1
    fi
    git tag -a "${tag}" -m "$1 $2"
}

# stage_beta_twin_sweep — a stable cut's marker commit also stages the
# DELETION of every component's beta twins, when gen-bootstraps.sh swept them
# (no versions/<comp>.beta.stamp = no open cycle). Otherwise a closed cycle's
# local deletion leaves the tree dirty and the next cut's origin guard refuses.
# Covers every component, not just the one being cut, for the same reason
# burrowee's public_components.sh exists: a sweep that only covers the cut
# component leaves ANOTHER component's just-closed twins unstaged.
stage_beta_twin_sweep() {
    local c
    for c in $(go run ./cmd/rkit components); do
        git add -A -- "${c}/beta.install.sh" "${c}/beta.version.js" 2>/dev/null || true
    done
}

# publish_preflight <comp> <stamp> <channel> — what both verbs run before
# they diverge: the staged dir, the module gate, the source worktree, and the
# cut-origin guard. Runs on a dry run too (report mode for the guard), and
# BEFORE either verb's dry-run branch returns.
#
# The module gate is the outer-bootstrap trust-chain gate: publishing
# regenerates the outer bootstrap and scp's it to the release host, so a stale
# committed bootstrap is precisely what this repo would otherwise ship — and a
# dry run that skipped the gate would report a publish as safe that is not.
# Which suites are in the set, and why the others are not, is documented in
# tools/module_gate.sh.
publish_preflight() {
    local comp="$1" stamp="$2" channel="$3"
    validate_stage "${comp}" "${stamp}"
    module_gate
    local src
    src="$(src_for "${comp}")"
    [ -d "${src}" ] || { echo "✗ ${comp} source worktree missing: ${src}" >&2; exit 1; }
    # Cut-origin guard (report mode on a dry run: findings print as ⚠ and
    # the rehearsal continues; strict otherwise).
    local mode=strict; [ "${DRY_RUN}" = 1 ] && mode=report
    assert_origins "${comp}" "${channel}" "${mode}"
}

# ---- distribute_only: the STABLE publish over an already-staged dist/<stamp>/
# (produced by `rkit build` — the produce half lives there now).
# Runs ONLY: tag + GitHub Release -> R2 mirror -> versions/<comp>.stamp ->
# gen-bootstraps.sh -> gen-version-jsonp.sh -> scp install.sh/version.js/
# pubkey/site to the release host -> [RELEASED] marker commit. No build, no
# sign, no notarize, no version bump — all of that already happened upstream
# in `rkit build`.
distribute_only() {
    local comp="$1" stamp="$2"
    local stage="${REPO_ROOT}/dist/${stamp}"
    publish_preflight "${comp}" "${stamp}" stable
    local src; src="$(src_for "${comp}")"

    if [ "${DRY_RUN}" = 1 ]; then
        echo "would: verify SHA256SUMS.txt.minisig against umbree-release.pub"
        echo "would: gh release create ${comp}/${stamp} (GitHub Release, public)"
        echo "would: mirror ${comp} to the R2 download mirror (when configured)"
        echo "would: write versions/${comp}.stamp = ${stamp} (the bootstrap's version floor)"
        echo "would: gen-bootstraps.sh (regenerate ${comp}/install.sh; sweep beta twins of closed cycles)"
        echo "would: gen-version-jsonp.sh ${comp} (regenerate ${comp}/version.js)"
        echo "would: scp install.sh/version.js/umbree-release.pub/site/index.html to ${RELEASE_HOST}:${STATIC_DIR}/${comp}/"
        echo "would: marker commit [RELEASED: ${comp}] ${stamp}"
        echo "✓ dry-run distribute-only: no real writes"
        return 0
    fi

    verify_release_key "${stage}"

    command -v "${GH_CLI}" >/dev/null 2>&1 \
        || { echo "✗ GitHub CLI not found: ${GH_CLI} (set UMBREE_GH to override)" >&2; exit 1; }
    "${GH_CLI}" repo view "${RELEASE_REPO}" --json name >/dev/null 2>&1 \
        || { echo "✗ ${GH_CLI} cannot access ${RELEASE_REPO} — check its authentication" >&2; exit 1; }
    require_release_host

    # (1) tag + GitHub Release.
    # Change summary: component commits since the previous release's source sha.
    # The stamp's trailing field IS the 8-char source sha, so the previous
    # release's sha is the suffix of the highest existing umbree/v… STABLE tag
    # (anchored: a beta tag is never the previous stable).
    local prev_tag prev_sha changes
    prev_tag="$(/usr/bin/git tag -l "${comp}/v*" --sort=version:refname \
        | grep -E "^${comp}/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}\$" | tail -n1 || true)"
    prev_sha="${prev_tag##*.}"
    if [ -n "${prev_sha}" ] && git -C "${src}" cat-file -e "${prev_sha}^{commit}" 2>/dev/null; then
        changes="$(git -C "${src}" log --oneline --no-merges "${prev_sha}..HEAD" 2>/dev/null)"
        [ -n "${changes}" ] || changes="No code changes since ${prev_tag} (re-release)."
    else
        changes="Initial release."
    fi

    local tag="${comp}/${stamp}"
    create_tag "${comp}" "${stamp}"

    local notes; notes="${stage}/release-notes.md"
    cat > "${notes}" <<NOTES
${comp} ${stamp} — $(date -u +%Y-%m-%d)

## Changes
${changes}

Install:
  curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/${comp}/install.sh | sh

Pin this version:
  UMBREE_VERSION=${tag} \\
    curl -fsSL https://release.umbree.org/${comp}/install.sh | sh

Verify by hand:
  minisign -Vm SHA256SUMS.txt -P "\$(cat umbree-release.pub | tail -n1)"
  f=<file>                                      # the file you downloaded
  want=\$(awk -v f="\$f" '{ n = \$2; sub(/^\\*/, "", n); if (n == f) { print \$1; exit } }' SHA256SUMS.txt)
  got=\$(shasum -a 256 "\$f" | awk '{print \$1}')  # sha256sum "\$f" on Linux
  if   [ -z "\$want" ];        then echo "NO ENTRY for \$f in SHA256SUMS.txt — do not install"
  elif [ "\$want" = "\$got" ];  then echo "OK \$f"
  else                             echo "MISMATCH for \$f — do not install"; fi
NOTES

    ( cd "${stage}" && "${GH_CLI}" -R "${RELEASE_REPO}" release create "${tag}" \
        --title "${comp} ${stamp}" --notes-file "${notes}" \
        "${comp}"-*.zip SHA256SUMS.txt SHA256SUMS.txt.minisig \
        "${REPO_ROOT}/umbree-release.pub" )

    # (2) mirror to R2. Must run BEFORE gen-version-jsonp.sh, which reads the
    # catalog this writes to learn the published stamp.
    mirror_r2 "${comp}" "${stamp}" "${stage}" stable

    # (3) the version floor, then regenerate bootstraps + version JSONP, then
    # scp the static surface. versions/<comp>.stamp is what gen-bootstraps.sh
    # bakes as MIN_VERSION; it must exist before the render.
    printf '%s\n' "${stamp}" > "${REPO_ROOT}/versions/${comp}.stamp"
    git add "versions/${comp}.stamp"
    bash "${REPO_ROOT}/tools/gen-bootstraps.sh" >&2
    bash "${REPO_ROOT}/tools/gen-version-jsonp.sh" "${comp}" >&2

    # shellcheck disable=SC2029  # ${STATIC_DIR}/${comp} are local, controlled values — expanding client-side into the remote command is intended.
    ssh "${RELEASE_HOST}" "mkdir -p '${STATIC_DIR}/${comp}'"
    scp -q "${REPO_ROOT}/${comp}/install.sh" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/install.sh"
    scp -q "${REPO_ROOT}/${comp}/version.js" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/version.js"
    if [ -f "${REPO_ROOT}/umbree-release.pub" ]; then
        scp -q "${REPO_ROOT}/umbree-release.pub" "${RELEASE_HOST}:${STATIC_DIR}/umbree-release.pub"
    fi
    if [ -f "${REPO_ROOT}/site/index.html" ]; then
        scp -q "${REPO_ROOT}/site/index.html" "${RELEASE_HOST}:${STATIC_DIR}/index.html"
    fi

    # (4) marker commit — the bump, the floor, the rendered surface, and the
    # sweep of any closed cycle's beta twins.
    git add "versions/${comp}" "versions/${comp}.stamp" "${comp}/install.sh" "${comp}/version.js"
    stage_beta_twin_sweep
    git commit --allow-empty -m "[RELEASED: ${comp}] $(date -u +%Y-%m-%d) ${stamp}"

    echo "✓ distributed ${tag}"
    echo "  Release: https://github.com/${RELEASE_REPO}/releases/tag/${tag}"
}

# ---- publish_beta: the BETA publish over an already-staged dist/<stamp>/.
# The steps of the header's "--channel beta" list, in order. R2-only, no
# GitHub Release, twins only to the static host.
publish_beta() {
    local comp="$1" stamp="$2"
    local stage="${REPO_ROOT}/dist/${stamp}"

    # The stamp must be beta-shaped: a stable-shaped stamp under the beta verb
    # would upload stable bytes under <comp>/beta/ and tag them as a beta.
    # Checked first — before the staged dir — because it is the cheapest way
    # to be holding the wrong verb.
    printf '%s\n' "${stamp}" \
        | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$' \
        || { echo "✗ not a beta stamp: ${stamp} (want v<X.Y.Z>.beta.<YYYY>.<MM>.<DD>.<sha8> — rkit build --channel beta produces it)" >&2; exit 1; }

    publish_preflight "${comp}" "${stamp}" beta

    # Beta must read newer than stable (tools/version.sh); refused before
    # anything else is touched, dry run included.
    bash "${REPO_ROOT}/tools/version.sh" "${comp}" --channel beta --assert-beta-above-stable || exit 1

    local bucket="${UMBREE_R2_BUCKET:-umbree-downloads}"
    if [ "${DRY_RUN}" = 1 ]; then
        echo "would: verify SHA256SUMS.txt.minisig against umbree-release.pub"
        r2_configured || echo "would: REFUSE — beta is R2-only and UMBREE_R2_ACCOUNT/UMBREE_R2_CREDS are not set"
        echo "would: git tag ${comp}/${stamp} and push the tag (no GitHub Release — beta is private, R2-only)"
        echo "would: upload to R2 bucket ${bucket}:"
        local f
        for f in "${stage}/${comp}"-*.zip "${stage}/SHA256SUMS.txt" "${stage}/SHA256SUMS.txt.minisig"; do
            [ -f "${f}" ] && echo "would:   ${comp}/beta/${stamp}/$(basename "${f}")"
        done
        echo "would:   ${comp}/beta/latest.json (last)"
        echo "would: write versions/${comp}.beta.stamp = ${stamp}"
        echo "would: gen-bootstraps.sh (render ${comp}/beta.install.sh)"
        echo "would: gen-version-jsonp.sh --channel beta ${comp} (render ${comp}/beta.version.js)"
        echo "would: scp ONLY beta.install.sh + beta.version.js to ${RELEASE_HOST}:${STATIC_DIR}/${comp}/"
        echo "would: marker commit [RELEASED: ${comp} beta] ${stamp} (private)"
        echo "✓ dry-run beta publish: no real writes"
        return 0
    fi

    verify_release_key "${stage}"
    require_r2
    require_release_host

    # (1) tag, pushed: history and what prune-releases.sh --channel beta
    # counts. Stable relies on `gh release create` to create the remote tag;
    # beta has no Release, so the push is explicit.
    local tag="${comp}/${stamp}"
    create_tag "${comp}" "${stamp}"
    git push origin "refs/tags/${tag}"

    # (2) R2 — artifacts, then <comp>/beta/latest.json last (r2-mirror).
    mirror_r2 "${comp}" "${stamp}" "${stage}" beta

    # (3) the beta stamp file (the twin's floor, and the open-cycle companion
    # of versions/<comp>.beta), then the twins, then ONLY the twins to the
    # host — the stable surface is not touched by a beta publish.
    printf '%s\n' "${stamp}" > "${REPO_ROOT}/versions/${comp}.beta.stamp"
    git add "versions/${comp}.beta.stamp"
    bash "${REPO_ROOT}/tools/gen-bootstraps.sh" >&2
    bash "${REPO_ROOT}/tools/gen-version-jsonp.sh" --channel beta "${comp}" >&2

    # shellcheck disable=SC2029  # local, controlled values — expanding client-side is intended.
    ssh "${RELEASE_HOST}" "mkdir -p '${STATIC_DIR}/${comp}'"
    scp -q "${REPO_ROOT}/${comp}/beta.install.sh" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/beta.install.sh"
    scp -q "${REPO_ROOT}/${comp}/beta.version.js" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/beta.version.js"

    # (4) marker commit.
    git add "versions/${comp}.beta" "versions/${comp}.beta.stamp" "${comp}/beta.install.sh" "${comp}/beta.version.js"
    git commit --allow-empty -m "[RELEASED: ${comp} beta] $(date -u +%Y-%m-%d) ${stamp} (private)"

    echo "✓ published beta ${tag} (R2-only; no GitHub Release)"
    echo "  Install: curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/${comp}/beta.install.sh | sh"
}

case "${VERB}" in
    distribute) distribute_only "${DIST_COMP}" "${DIST_STAMP}" ;;
    beta)       publish_beta "${DIST_COMP}" "${DIST_STAMP}" ;;
esac
