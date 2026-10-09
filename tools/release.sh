#!/usr/bin/env bash
set -euo pipefail

read -r -d '' HELP_TEXT <<'HELP' || true
release.sh — PUBLISH an already-staged umbree|umbreed release.

Usage:
  bash tools/release.sh --distribute-only <umbree|umbreed> <stamp> [--dry-run]   # stable publish
  bash tools/release.sh --channel beta     <umbree|umbreed> <stamp> [--dry-run]   # beta publish

This repo has NO shell build path. Building, signing, and (with --apple)
notarizing the artifact set live entirely in `rkit build` (the produce half),
which stamps + cross-compiles the component for darwin/{arm64,amd64} +
linux/{arm64,amd64}, writes SHA256SUMS.txt, and minisign-signs it into
dist/<stamp>/.

Both verbs publish THAT already-staged dist/<stamp>/ WITHOUT building,
signing, notarizing, or bumping a version — they run only the publish half.
They share the pre-flight (staged dir, module gate, cut-origin guard with its
main→dev sync-back check, the release key check) and differ in where the
bytes go:

--distribute-only (STABLE):
  1. git-tags <comp>/<stamp> + publishes a GitHub Release on umbree-git/release.
  2. mirrors the artifacts to the R2 download mirror and rewrites its catalog,
     when the mirror is configured — skipped, loudly, when it is not.
  3. records versions/<comp>.stamp (the version floor the bootstrap bakes),
     regenerates the outer bootstrap + version JSONP and scp's the static
     surface (install.sh, version.js, umbree-release.pub, site/index.html)
     to the release host.
  4. records a [RELEASED: <comp>] marker commit.
  GitHub Releases host the zips and stay primary; the R2 mirror is a fallback
  and the source of the published-stamp catalog.

--channel beta (BETA — beta.md; "private" = R2-only, no GitHub Release):
  1. asserts versions/<comp>.beta sorts above versions/<comp>, and that the
     stamp is beta-shaped (v<X.Y.Z>.beta.<date>.<sha8>).
  2. REQUIRES the R2 mirror: a beta cut creates no GitHub Release, so an
     unconfigured mirror is a refusal (exit 1, before any write), not a skip.
  3. git-tags <comp>/<stamp> and pushes the tag (history; what
     prune-releases.sh --channel beta counts) — no Release is created, so the
     stable bootstrap's /releases resolution never sees it.
  4. uploads every artifact to R2 <comp>/beta/<stamp>/<file>, then
     <comp>/beta/latest.json LAST.
  5. records versions/<comp>.beta.stamp, renders <comp>/beta.install.sh and
     <comp>/beta.version.js (the twins feature 02's `umbree update` fetches —
     their names and URLs never move), and scp's ONLY those two to the
     release host. install.sh / version.js / the pubkey / site are the stable
     surface and are not touched.
  6. records a [RELEASED: <comp> beta] … (private) marker commit.

There is no console/dispatcher. Opening, approving and closing a cycle are
operator steps (tools/version.sh --seed; tools/adopt-beta-version.sh;
README "Beta channel") — nothing here does them.

On --dry-run: validates the staged dir + component, runs the gates in
report mode, then prints "would: ..." for every publish action and returns —
no GitHub/git/ssh/scp/network writes.

Env:
  RELEASE_HOST           ssh alias for the nginx static host — REQUIRED (no default --
                          this repo is public, so a default would ship the production
                          hostname)
  STATIC_DIR             absolute static dir on that host — REQUIRED (no default,
                          same reason)
  UMBREE_SRC_UMBREE      umbree component source worktree — REQUIRED (no default) when
                          distributing umbree. The cut-origin guard requires it to BE
                          the registry main folder (<brand>/cli/code/main) on stable, or
                          its code/beta sibling worktree on beta; a different tree is
                          permitted only under --dry-run
  UMBREE_SRC_UMBREED     umbreed component source worktree — REQUIRED (no default) when
                          distributing umbreed; same rule (<brand>/daemon/code/{main,beta})
  BETA_BRANCH            beta only — the branch the beta worktree must be on
                          (default: config/beta-branch, else "beta"; beta.md §2)
  UMBREE_RELEASE_REPO    GitHub repo for releases (default umbree-git/release)
  UMBREE_GH              GitHub CLI to publish with (default `gh`) — set it when
                          your environment provides a different one
  UMBREE_R2_ACCOUNT      Cloudflare account id for the download mirror. Unset =
                          stable skips the mirror (GitHub remains the only channel);
                          beta REFUSES
  UMBREE_R2_CREDS        path to the R2 S3 credentials TOML. Unset = same
  UMBREE_R2_BUCKET       mirror bucket (default umbree-downloads)
  UMBREE_R2_GATED_BUCKET the private gated bucket a stable cut stages to before any
                          public act — REQUIRED for a stable cut (no default, never
                          the public bucket); its name lives in the sealed config only.
                          The same UMBREE_R2_ACCOUNT/UMBREE_R2_CREDS token serves it
HELP

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# shellcheck source=tools/module_gate.sh
source "${REPO_ROOT}/tools/module_gate.sh"
# shellcheck source=tools/release_origin.sh
source "${REPO_ROOT}/tools/release_origin.sh"

usage() { echo "✗ usage: release.sh --distribute-only <umbree|umbreed> <stamp> [--dry-run]   (stable)
         release.sh --channel beta     <umbree|umbreed> <stamp> [--dry-run]   (beta)" >&2; }
print_help() { printf '%s\n' "${HELP_TEXT}"; }

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

RELEASE_HOST="${RELEASE_HOST:?set RELEASE_HOST to the ssh alias for the nginx static host}"
STATIC_DIR="${STATIC_DIR:?set STATIC_DIR to the absolute static dir on that host}"
RELEASE_REPO="${UMBREE_RELEASE_REPO:-umbree-git/release}"

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

registry_main_for() {
    local brand_root
    brand_root="$(cd "${REPO_ROOT}/../../.." 2>/dev/null && pwd)" || brand_root="${REPO_ROOT}/../../.."
    case "$1" in
        umbree)  printf '%s/cli/code/main' "${brand_root}" ;;
        umbreed) printf '%s/daemon/code/main' "${brand_root}" ;;
    esac
}

assert_origins() {
    local comp="$1" channel="$2" mode="$3" reg src
    reg="$(registry_main_for "${comp}")"; src="$(src_for "${comp}")"
    if [ "${channel}" = beta ]; then
        assert_release_origin "${comp}" "${src}" "${reg}" "${mode}" beta || exit 1
    else
        assert_release_origin "${comp}" "${src}" "${reg}" "${mode}" || exit 1
    fi
    check_sync_back "${comp}" "${src}" "${mode}" || exit 1
    local -a staged=()
    while IFS= read -r p; do [ -n "$p" ] && staged+=("$p"); done <<EOF
$(staged_tolerance_for 1 "${comp}" "${channel}")
EOF
    assert_release_origin "release repo" "${REPO_ROOT}" "${REPO_ROOT}" "${mode}" ${staged[@]+"${staged[@]}"} || exit 1
    check_sync_back "release repo" "${REPO_ROOT}" "${mode}" || exit 1
}

GH_CLI="${UMBREE_GH:-gh}"

r2_configured() {
    [ -n "${UMBREE_R2_ACCOUNT:-}" ] && [ -n "${UMBREE_R2_CREDS:-}" ] && [ -f "${UMBREE_R2_CREDS}" ]
}

apply_retention() {
    local comp="$1" channel="$2"
    echo
    echo "→ retention (applying ${channel}):"
    env -u KEEP CHANNEL="${channel}" COMPONENTS="${comp}" \
        bash "${REPO_ROOT}/tools/prune-releases.sh" --execute || true
    if r2_configured; then
        ( cd "${REPO_ROOT}/tools/r2-mirror" && "${GO_BIN:-go}" run ./cmd/r2-prune \
            --comp "${comp}" --channel "${channel}" --execute ) || true
    fi
}

gated_channel_for() {
    case "$1" in
        stable) printf 'production\n' ;;
        *) echo "✗ no gated channel for '$1' (only a stable cut stages to the gated store)" >&2; return 1 ;;
    esac
}

require_gated() {
    [ -n "${UMBREE_R2_GATED_BUCKET:-}" ] || {
        echo "✗ UMBREE_R2_GATED_BUCKET is not set — a stable cut stages its bytes to the private gated store before any public act; set it from the sealed configuration (there is no default, and the public bucket is never used in its place)" >&2
        exit 1
    }
    r2_configured || {
        echo "✗ the gated store needs UMBREE_R2_ACCOUNT and UMBREE_R2_CREDS (one R2 token serves both buckets) — nothing published" >&2
        exit 1
    }
}

stage_gated() {
    local comp="$1" stamp="$2" stage="$3" channel semver
    channel="$(gated_channel_for stable)"
    semver="$(cat "${REPO_ROOT}/versions/${comp}")"
    echo "→ staging ${comp} ${stamp} to the gated store (${channel})" >&2
    if ( cd "${REPO_ROOT}/tools/r2-mirror" && "${GO_BIN:-go}" run . --store gated \
            --account "${UMBREE_R2_ACCOUNT}" --bucket "${UMBREE_R2_GATED_BUCKET}" \
            --stage-dir "${stage}" --comp "${comp}" --channel "${channel}" \
            --version "${semver}" --stamp "${stamp}" --creds "${UMBREE_R2_CREDS}" \
            --receipt "${stage}/gated-receipt.json" >&2 ); then
        echo "✓ staged ${comp} to the gated store (receipt: dist/${stamp}/gated-receipt.json)" >&2
        return 0
    fi
    echo "✗ gated stage FAILED for ${comp} ${stamp} — nothing published (no tag, no GitHub Release, no mirror, no static surface); fix the cause and re-run" >&2
    exit 1
}

gated_dry_run() {
    local comp="$1" stamp="$2" stage="$3" channel f
    channel="$(gated_channel_for stable)"
    if [ -z "${UMBREE_R2_GATED_BUCKET:-}" ]; then
        echo "would: REFUSE — UMBREE_R2_GATED_BUCKET is not set (a stable cut stages to the gated store first)"
    fi
    for f in "${stage}/SHA256SUMS.txt" "${stage}/SHA256SUMS.txt.minisig" "${stage}/${comp}"-*.zip; do
        [ -f "${f}" ] && echo "would: stage ${comp}/${channel}/${stamp}/$(basename "${f}")"
    done
    echo "would: no manifest in the gated store"
}

require_r2() {
    r2_configured || {
        echo "✗ beta is R2-only — set UMBREE_R2_ACCOUNT and UMBREE_R2_CREDS (a beta cut publishes no GitHub Release, so there is nothing else to serve it from)" >&2
        exit 1
    }
}

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

verify_release_key() {
    command -v minisign >/dev/null 2>&1 || { echo "✗ required tool not found: minisign" >&2; exit 1; }
    minisign -V -p "${REPO_ROOT}/umbree-release.pub" \
        -m "$1/SHA256SUMS.txt" -x "$1/SHA256SUMS.txt.minisig" >/dev/null 2>&1 \
        || { echo "✗ staged dist is not signed by the release key — re-run 'rkit build --apple --sign-key <real key>'" >&2; exit 1; }
}

require_release_host() {
    ssh -o BatchMode=yes -o ConnectTimeout=5 "${RELEASE_HOST}" 'true' 2>/dev/null \
        || { echo "✗ cannot ssh to ${RELEASE_HOST}" >&2; exit 1; }
}

create_tag() {
    local tag="$1/$2"
    if git rev-parse "refs/tags/${tag}" >/dev/null 2>&1; then
        echo "✗ tag ${tag} already exists locally" >&2
        exit 1
    fi
    git tag -a "${tag}" -m "$1 $2"
}

stage_beta_twin_sweep() {
    local c
    for c in $(go run ./cmd/rkit components); do
        git add -A -- "${c}/beta.install.sh" "${c}/beta.version.js" 2>/dev/null || true
    done
}

publish_preflight() {
    local comp="$1" stamp="$2" channel="$3"
    validate_stage "${comp}" "${stamp}"
    module_gate
    local src
    src="$(src_for "${comp}")"
    [ -d "${src}" ] || { echo "✗ ${comp} source worktree missing: ${src}" >&2; exit 1; }
    local mode=strict; [ "${DRY_RUN}" = 1 ] && mode=report
    assert_origins "${comp}" "${channel}" "${mode}"
}

distribute_dry_run() {
    local comp="$1" stamp="$2"
    echo "would: verify SHA256SUMS.txt.minisig against umbree-release.pub"
    gated_dry_run "${comp}" "${stamp}" "${REPO_ROOT}/dist/${stamp}"
    echo "would: gh release create ${comp}/${stamp} (GitHub Release, public)"
    echo "would: mirror ${comp} to the R2 download mirror (when configured)"
    echo "would: write versions/${comp}.stamp = ${stamp} (the bootstrap's version floor)"
    echo "would: gen-bootstraps.sh (regenerate ${comp}/install.sh; sweep beta twins of closed cycles)"
    echo "would: gen-version-jsonp.sh ${comp} (regenerate ${comp}/version.js)"
    echo "would: scp install.sh/version.js/umbree-release.pub/site/index.html to ${RELEASE_HOST}:${STATIC_DIR}/${comp}/"
    echo "would: marker commit [RELEASED: ${comp}] ${stamp}"
    echo "✓ dry-run distribute-only: no real writes"
}

distribute_preflight() {
    local stage="$1"
    verify_release_key "${stage}"
    require_gated

    command -v "${GH_CLI}" >/dev/null 2>&1 \
        || { echo "✗ GitHub CLI not found: ${GH_CLI} (set UMBREE_GH to override)" >&2; exit 1; }
    "${GH_CLI}" repo view "${RELEASE_REPO}" --json name >/dev/null 2>&1 \
        || { echo "✗ ${GH_CLI} cannot access ${RELEASE_REPO} — check its authentication" >&2; exit 1; }
    require_release_host
}

release_changes() {
    local comp="$1" src="$2"
    local prev_tag prev_sha changes
    prev_tag="$(/usr/bin/git tag -l "${comp}/v*" --sort=version:refname \
        | grep -E "^${comp}/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}\$" | tail -n1 || true)"
    prev_sha="${prev_tag##*.}"
    if [ -n "${prev_sha}" ] && git -C "${src}" cat-file -e "${prev_sha}^{commit}" 2>/dev/null; then
        changes="$(git -C "${src}" log --oneline --no-merges "${prev_sha}..HEAD" 2>/dev/null)" || return 1
        [ -n "${changes}" ] || changes="No code changes since ${prev_tag} (re-release)."
    else
        changes="Initial release."
    fi
    printf '%s\n' "${changes}"
}

write_release_notes() {
    local comp="$1" stamp="$2" notes="$3" changes="$4"
    local tag="${comp}/${stamp}"
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
}

stage_and_publish() {
    local comp="$1" stamp="$2" stage="$3" changes="$4"
    stage_gated "${comp}" "${stamp}" "${stage}"
    local tag="${comp}/${stamp}"
    create_tag "${comp}" "${stamp}"
    local notes; notes="${stage}/release-notes.md"
    write_release_notes "${comp}" "${stamp}" "${notes}" "${changes}"
    ( cd "${stage}" && "${GH_CLI}" -R "${RELEASE_REPO}" release create "${tag}" \
        --title "${comp} ${stamp}" --notes-file "${notes}" \
        "${comp}"-*.zip SHA256SUMS.txt SHA256SUMS.txt.minisig \
        "${REPO_ROOT}/umbree-release.pub" )

    mirror_r2 "${comp}" "${stamp}" "${stage}" stable
}

mark_release() {
    local comp="$1" stamp="$2"
    local tag="${comp}/${stamp}"
    printf '%s\n' "${stamp}" > "${REPO_ROOT}/versions/${comp}.stamp"
    git add "versions/${comp}.stamp"
    bash "${REPO_ROOT}/tools/gen-bootstraps.sh" >&2
    bash "${REPO_ROOT}/tools/gen-version-jsonp.sh" "${comp}" >&2

    # shellcheck disable=SC2029
    ssh "${RELEASE_HOST}" "mkdir -p '${STATIC_DIR}/${comp}'"
    scp -q "${REPO_ROOT}/${comp}/install.sh" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/install.sh"
    scp -q "${REPO_ROOT}/${comp}/version.js" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/version.js"
    if [ -f "${REPO_ROOT}/umbree-release.pub" ]; then
        scp -q "${REPO_ROOT}/umbree-release.pub" "${RELEASE_HOST}:${STATIC_DIR}/umbree-release.pub"
    fi
    if [ -f "${REPO_ROOT}/site/index.html" ]; then
        scp -q "${REPO_ROOT}/site/index.html" "${RELEASE_HOST}:${STATIC_DIR}/index.html"
    fi

    git add "versions/${comp}" "versions/${comp}.stamp" "${comp}/install.sh" "${comp}/version.js"
    stage_beta_twin_sweep
    git commit --allow-empty -m "[RELEASED: ${comp}] $(date -u +%Y-%m-%d) ${stamp}"

    apply_retention "${comp}" stable

    echo "✓ distributed ${tag}"
    echo "  Release: https://github.com/${RELEASE_REPO}/releases/tag/${tag}"
}

distribute_only() {
    local comp="$1" stamp="$2"
    local stage="${REPO_ROOT}/dist/${stamp}"
    publish_preflight "${comp}" "${stamp}" stable
    if [ "${DRY_RUN}" = 1 ]; then
        distribute_dry_run "${comp}" "${stamp}"
        return 0
    fi
    local src changes; src="$(src_for "${comp}")"
    changes="$(release_changes "${comp}" "${src}")" || {
        echo "✗ cannot read the change summary for ${comp} from ${src} (git log failed) — nothing published" >&2
        exit 1
    }
    distribute_preflight "${stage}"
    stage_and_publish "${comp}" "${stamp}" "${stage}" "${changes}"
    mark_release "${comp}" "${stamp}"
}

publish_beta() {
    local comp="$1" stamp="$2"
    local stage="${REPO_ROOT}/dist/${stamp}"

    printf '%s\n' "${stamp}" \
        | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$' \
        || { echo "✗ not a beta stamp: ${stamp} (want v<X.Y.Z>.beta.<YYYY>.<MM>.<DD>.<sha8> — rkit build --channel beta produces it)" >&2; exit 1; }

    publish_preflight "${comp}" "${stamp}" beta

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

    local tag="${comp}/${stamp}"
    create_tag "${comp}" "${stamp}"
    git push origin "refs/tags/${tag}" || {
        git tag -d "${tag}" >/dev/null 2>&1 || true
        echo "✗ could not push tag ${tag} — nothing published; the local tag was removed so a re-run starts clean" >&2
        exit 1
    }

    mirror_r2 "${comp}" "${stamp}" "${stage}" beta

    printf '%s\n' "${stamp}" > "${REPO_ROOT}/versions/${comp}.beta.stamp"
    git add "versions/${comp}.beta.stamp"
    bash "${REPO_ROOT}/tools/gen-bootstraps.sh" >&2
    bash "${REPO_ROOT}/tools/gen-version-jsonp.sh" --channel beta "${comp}" >&2

    # shellcheck disable=SC2029
    ssh "${RELEASE_HOST}" "mkdir -p '${STATIC_DIR}/${comp}'"
    scp -q "${REPO_ROOT}/${comp}/beta.install.sh" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/beta.install.sh"
    scp -q "${REPO_ROOT}/${comp}/beta.version.js" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/beta.version.js"

    git add "versions/${comp}.beta" "versions/${comp}.beta.stamp" "${comp}/beta.install.sh" "${comp}/beta.version.js"
    git commit --allow-empty -m "[RELEASED: ${comp} beta] $(date -u +%Y-%m-%d) ${stamp} (private)"

    apply_retention "${comp}" beta

    echo "✓ published beta ${tag} (R2-only; no GitHub Release)"
    echo "  Install: curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/${comp}/beta.install.sh | sh"
}

case "${VERB}" in
    distribute) distribute_only "${DIST_COMP}" "${DIST_STAMP}" ;;
    beta)       publish_beta "${DIST_COMP}" "${DIST_STAMP}" ;;
esac
