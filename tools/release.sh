#!/usr/bin/env bash
set -euo pipefail

read -r -d '' HELP_TEXT <<'HELP' || true
release.sh — CUT an already-built umbree|umbreed release: stage it to the gated store and register it.

Usage:
  bash tools/release.sh --distribute-only <umbree|umbreed> <stamp> [--dry-run]   # stable cut
  bash tools/release.sh --register-only <umbree|umbreed> <stamp> [--dry-run]     # re-register a staged cut
  bash tools/release.sh --channel beta     <umbree|umbreed> <stamp> [--dry-run]   # dormant beta publish

This repo has NO shell build path. Building, signing, and (with --apple)
notarizing the artifact set live entirely in `rkit build` (the produce half),
which stamps + cross-compiles the component for darwin/{arm64,amd64} +
linux/{arm64,amd64}, writes SHA256SUMS.txt, and minisign-signs it into
dist/<stamp>/. Nothing here builds, signs, notarizes or bumps a version.

--distribute-only (STABLE) makes nothing public. After the pre-flight (staged
dir, module gate, cut-origin guard with its main→dev sync-back check, the
release key check, the gated store and manage service settings, and no
existing local tag) it:
  1. stages every artifact to the private gated store under
     <comp>/production/<stamp>/ and writes dist/<stamp>/gated-receipt.json;
  2. registers the row as staged with the manage service (rkit register,
     signed with the release key), then reads it back (rkit status). A
     refusal, or a row that does not read back staged with this stamp, stops
     the cut here with the re-register command; nothing is tagged or marked;
  3. tags <comp>/<stamp> and pushes the tag. No GitHub Release is made;
  4. records a [RELEASED: <comp>] marker commit carrying versions/<comp>;
  5. reports the stamp, the row and its page in the manage console.
Going public is the operator's promote in the manage console: it copies the
bytes to the public surface, writes <comp>/latest.json last, and republishes
the static surface (install.sh, version.js, the public key, the site page).
versions/<comp>.stamp, the version floor the installers bake, follows the
promote through tools/record-promoted.sh and is never written by a cut.

--register-only re-runs step 2 alone, for bytes a cut already staged
(dist/<stamp>/gated-receipt.json). It tags and marks nothing.

--channel beta is dormant: Umbree has no beta stage. Unlike the stable verb
it would publish at the cut, to the public download bucket, with no gated
store or promote in front of it: tag and push, upload to <comp>/beta/<stamp>/
with <comp>/beta/latest.json last, render and scp the beta.install.sh and
beta.version.js twins to RELEASE_HOST:STATIC_DIR, and a
[RELEASED: <comp> beta] … (private) marker commit.

On --dry-run: validates the staged dir + component, runs the gates in
report mode, then prints "would: ..." for every step and returns — no
network, tag, upload or commit.

Env:
  UMBREE_SRC_UMBREE      umbree component source worktree — REQUIRED (no default) when
                          cutting umbree. The cut-origin guard requires it to BE
                          the registry main folder (<brand>/cli/code/main); a
                          different tree is permitted only under --dry-run
  UMBREE_SRC_UMBREED     umbreed component source worktree — REQUIRED (no default) when
                          cutting umbreed; same rule (<brand>/daemon/code/main)
  UMBREE_R2_ACCOUNT      Cloudflare account id; with UMBREE_R2_CREDS it is the one R2
                          token that serves both buckets
  UMBREE_R2_CREDS        path to the R2 S3 credentials TOML
  UMBREE_R2_GATED_BUCKET the private gated bucket a stable cut stages to — REQUIRED
                          for a stable cut (no default, never the public bucket);
                          its name lives in the sealed configuration only
  UMBREE_MANAGE_URL      the manage service's https base the cut registers with —
                          REQUIRED for a stable cut; sealed configuration only
  UMBREE_RELEASE_KEY     the decrypted release key file registration is signed with —
                          REQUIRED for a stable cut; release.command sets it
  RELEASE_HOST           beta only: ssh alias for the nginx static host (no default)
  STATIC_DIR             beta only: absolute static dir on that host (no default)
  BETA_BRANCH            beta only — the branch the beta worktree must be on
                          (default: config/beta-branch, else "beta"; beta.md §2)
  UMBREE_R2_BUCKET       mirror bucket (default umbree-downloads); beta only
HELP

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# shellcheck source=tools/module_gate.sh
source "${REPO_ROOT}/tools/module_gate.sh"
# shellcheck source=tools/release_origin.sh
source "${REPO_ROOT}/tools/release_origin.sh"

usage() { echo "✗ usage: release.sh --distribute-only <umbree|umbreed> <stamp> [--dry-run]   (stable cut)
         release.sh --register-only <umbree|umbreed> <stamp> [--dry-run]     (re-register a staged cut)
         release.sh --channel beta     <umbree|umbreed> <stamp> [--dry-run]   (dormant beta)" >&2; }
print_help() { printf '%s\n' "${HELP_TEXT}"; }

CHANNEL=stable; VERB=""; DIST_COMP=""; DIST_STAMP=""
case "${1:-}" in
    --distribute-only|--register-only)
        VERB=distribute; [ "$1" = --register-only ] && VERB=register
        shift
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
            if [ "${VERB}" = distribute ] || [ "${VERB}" = register ]; then
                echo "✗ --distribute-only is a stable-channel verb; a beta cut publishes to R2 with: release.sh --channel beta <comp> <stamp>" >&2
            else
                echo "✗ --channel is given once, as the verb" >&2
            fi
            exit 2 ;;
        *) echo "✗ ${VERB} accepts only --dry-run (got '${arg}')" >&2; exit 2 ;;
    esac
done

if [ "${VERB}" = beta ]; then
    RELEASE_HOST="${RELEASE_HOST:?set RELEASE_HOST to the ssh alias for the nginx static host}"
    STATIC_DIR="${STATIC_DIR:?set STATIC_DIR to the absolute static dir on that host}"
fi
CUT_ROW=""
ROW_LINE=""
ROW_VERSION=""

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

r2_configured() {
    [ -n "${UMBREE_R2_ACCOUNT:-}" ] && [ -n "${UMBREE_R2_CREDS:-}" ] && [ -f "${UMBREE_R2_CREDS}" ]
}

apply_retention() {
    local comp="$1" channel="$2"
    echo
    echo "→ retention (applying ${channel}):"
    if [ "${channel}" = beta ]; then
        env -u KEEP CHANNEL="${channel}" COMPONENTS="${comp}" \
            bash "${REPO_ROOT}/tools/prune-releases.sh" --execute || true
    fi
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
    echo "✗ gated stage FAILED for ${comp} ${stamp} — nothing registered, tagged or marked, and nothing is public; fix the cause and re-run" >&2
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
        echo "⚠ R2 mirror skipped: UMBREE_R2_ACCOUNT/UMBREE_R2_CREDS not set" >&2
        return 0
    fi
    if [ ! -f "${creds}" ]; then
        echo "⚠ R2 mirror skipped: credentials file not found" >&2
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
        echo "  The mirror is the dormant beta verb's; the stable cut never calls it." >&2
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

require_manage() {
    case "${UMBREE_MANAGE_URL:-}" in
        https://?*) ;;
        "")
            echo "✗ UMBREE_MANAGE_URL is not set — a stable cut registers its staged row with the manage service; set it from the sealed configuration" >&2
            exit 1 ;;
        *)
            echo "✗ UMBREE_MANAGE_URL must be an https:// URL (got '${UMBREE_MANAGE_URL}') — a release row is never registered over a connection anyone can read" >&2
            exit 1 ;;
    esac
    [ -n "${UMBREE_RELEASE_KEY:-}" ] && [ -r "${UMBREE_RELEASE_KEY}" ] || {
        echo "✗ UMBREE_RELEASE_KEY must name the readable, decrypted release key file — registration is signed with it (release.command sets it)" >&2
        exit 1
    }
}

manage_dry_run() {
    case "${UMBREE_MANAGE_URL:-}" in
        https://?*) ;;
        "") echo "would: REFUSE — UMBREE_MANAGE_URL is not set (a stable cut registers its row with the manage service)" ;;
        *) echo "would: REFUSE — UMBREE_MANAGE_URL is not an https:// URL" ;;
    esac
}

refuse_existing_tag() {
    local tag="$1/$2"
    if git rev-parse --quiet --verify "refs/tags/${tag}" >/dev/null 2>&1; then
        echo "✗ tag ${tag} already exists locally — this stamp was cut before; nothing staged" >&2
        exit 1
    fi
}

valid_cut_args() {
    local comp="$1" stamp="$2"
    case "${comp}" in
        umbree|umbreed) ;;
        *) echo "✗ unknown component: ${comp}" >&2; exit 1 ;;
    esac
    printf '%s\n' "${stamp}" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$' \
        || { echo "✗ not a stable stamp: ${stamp} (want v<X.Y.Z>.<YYYY>.<MM>.<DD>.<sha8>)" >&2; exit 1; }
}

run_rkit() {
    ( cd "${REPO_ROOT}" && "${GO_BIN:-go}" run ./cmd/rkit "$@" )
}

registration_failed() {
    local comp="$1" stamp="$2" why="$3" state="$4"
    echo "✗ registration FAILED for ${comp} ${stamp}: ${why}" >&2
    echo "  ${state}" >&2
    echo "  re-register with: bash tools/release.sh --register-only ${comp} ${stamp}" >&2
    echo "  nothing is tagged or marked; once the row reads back staged, tools/RUNBOOK.md has the tag and marker steps" >&2
    exit 1
}

UNKNOWN_ROW="the bytes are in the gated store; a row may or may not exist, and --register-only reads it back before it registers again"

register_staged() {
    local comp="$1" stamp="$2" stage="$3" tolerate="${4:-}" semver out rc=0
    semver="$(cat "${REPO_ROOT}/versions/${comp}")"
    echo "→ registering ${comp} ${stamp} as staged with the manage service" >&2
    out="$(run_rkit register --manage-url "${UMBREE_MANAGE_URL}" --sign-key "${UMBREE_RELEASE_KEY}" \
        --receipt "${stage}/gated-receipt.json" --component "${comp}" \
        --channel "$(gated_channel_for stable)" --version "${semver}" --stamp "${stamp}" 2>&1)" || rc=$?
    [ -z "${out}" ] || printf '%s\n' "${out}" >&2
    [ "${rc}" -eq 0 ] && return 0
    if [ -n "${tolerate}" ] && printf '%s' "${out}" | grep -q 'HTTP 409'; then
        echo "→ the service already catalogues ${comp} ${stamp}; reading the row back" >&2
        return 0
    fi
    registration_failed "${comp}" "${stamp}" "the manage service refused it" "${UNKNOWN_ROW}"
}

read_row() {
    local comp="$1" stamp="$2" word id state version got extra
    ROW_LINE="$(run_rkit status --manage-url "${UMBREE_MANAGE_URL}" --sign-key "${UMBREE_RELEASE_KEY}" \
        --component "${comp}" --channel "$(gated_channel_for stable)" --stamp "${stamp}")" || return 1
    if [ "$(printf '%s\n' "${ROW_LINE}" | wc -l | tr -d ' ')" != 1 ]; then
        return 2
    fi
    read -r word id state version got extra <<EOF
${ROW_LINE}
EOF
    if [ "${word}" != row ] || ! printf '%s' "${id}" | grep -Eq '^[0-9]+$' \
        || [ "${state}" != staged ] || [ "${got}" != "${stamp}" ] || [ -n "${extra}" ]; then
        return 2
    fi
    CUT_ROW="${id}"
    ROW_VERSION="${version}"
}

confirm_row() {
    local comp="$1" stamp="$2" rc=0
    read_row "${comp}" "${stamp}" || rc=$?
    case "${rc}" in
        0) echo "✓ row ${CUT_ROW} reads back staged: ${comp} ${ROW_VERSION} ${stamp}" >&2 ;;
        1) registration_failed "${comp}" "${stamp}" "the row could not be read back" "${UNKNOWN_ROW}" ;;
        *) registration_failed "${comp}" "${stamp}" "the service reads it back as '${ROW_LINE}'" \
            "the bytes are in the gated store; the row read back is not this cut's staged row" ;;
    esac
}

stage_and_register() {
    local comp="$1" stamp="$2" stage="$3"
    stage_gated "${comp}" "${stamp}" "${stage}"
    register_staged "${comp}" "${stamp}" "${stage}"
    confirm_row "${comp}" "${stamp}"
}

tag_and_mark() {
    local comp="$1" stamp="$2" tag="$1/$2"
    create_tag "${comp}" "${stamp}"
    git push origin "refs/tags/${tag}" || {
        git tag -d "${tag}" >/dev/null 2>&1 || true
        echo "✗ could not push tag ${tag} — row ${CUT_ROW} is registered and staged, nothing is public; the local tag was removed, so push it by hand and make the [RELEASED: ${comp}] marker commit (tools/RUNBOOK.md)" >&2
        exit 1
    }
    git add "versions/${comp}"
    git commit --allow-empty -m "[RELEASED: ${comp}] $(date -u +%Y-%m-%d) ${stamp}"
}

report_cut() {
    local comp="$1" stamp="$2" row="$3"
    echo "✓ cut ${comp} ${stamp}: staged in the gated store and registered as row ${row} — nothing is public"
    echo "  Promote it in the manage console: ${UMBREE_MANAGE_URL%/}/manage/$(gated_channel_for stable)/${comp} (row ${row})"
    echo "  After the promote: tools/promote-check.sh ${comp} stable --expect ${stamp}, then tools/record-promoted.sh ${comp} ${stamp}"
}

distribute_dry_run() {
    local comp="$1" stamp="$2"
    echo "would: verify SHA256SUMS.txt.minisig against umbree-release.pub"
    gated_dry_run "${comp}" "${stamp}" "${REPO_ROOT}/dist/${stamp}"
    manage_dry_run
    echo "would: register ${comp} ${stamp} as staged with the manage service"
    echo "would: confirm row ${comp} ${stamp} reads back staged"
    echo "would: tag ${comp}/${stamp} and push the tag (no GitHub Release)"
    echo "would: marker commit [RELEASED: ${comp}] ${stamp} (versions/${comp} only)"
    echo "✓ dry-run distribute-only: nothing staged, registered, tagged or committed"
}

distribute_preflight() {
    local comp="$1" stamp="$2" stage="$3"
    verify_release_key "${stage}"
    require_gated
    require_manage
    refuse_existing_tag "${comp}" "${stamp}"
}

distribute_only() {
    local comp="$1" stamp="$2"
    local stage="${REPO_ROOT}/dist/${stamp}"
    publish_preflight "${comp}" "${stamp}" stable
    if [ "${DRY_RUN}" = 1 ]; then
        distribute_dry_run "${comp}" "${stamp}"
        return 0
    fi
    distribute_preflight "${comp}" "${stamp}" "${stage}"
    stage_and_register "${comp}" "${stamp}" "${stage}"
    tag_and_mark "${comp}" "${stamp}"
    report_cut "${comp}" "${stamp}" "${CUT_ROW}"
}

register_only() {
    local comp="$1" stamp="$2"
    local stage="${REPO_ROOT}/dist/${stamp}"
    valid_cut_args "${comp}" "${stamp}"
    [ -f "${stage}/gated-receipt.json" ] || {
        echo "✗ no dist/${stamp}/gated-receipt.json — --register-only registers bytes a cut already staged, and this stamp was never staged here" >&2
        exit 1
    }
    if [ "${DRY_RUN}" = 1 ]; then
        manage_dry_run
        echo "would: register ${comp} ${stamp} as staged with the manage service"
        echo "would: confirm row ${comp} ${stamp} reads back staged"
        return 0
    fi
    require_manage
    local rc=0
    read_row "${comp}" "${stamp}" || rc=$?
    case "${rc}" in
        0) echo "✓ ${comp} ${stamp} is already registered as row ${CUT_ROW}, staged — nothing registered, tagged or marked; tools/RUNBOOK.md has the steps that finish the cut" ;;
        1)
            register_staged "${comp}" "${stamp}" "${stage}" tolerate-409
            confirm_row "${comp}" "${stamp}"
            echo "✓ registered ${comp} ${stamp} as row ${CUT_ROW} — nothing tagged or marked; tools/RUNBOOK.md has the steps that finish the cut" ;;
        *) registration_failed "${comp}" "${stamp}" "the service reads it back as '${ROW_LINE}'" \
            "the bytes are in the gated store; the row read back is not this cut's staged row, so nothing was registered" ;;
    esac
    echo "  Promote it in the manage console: ${UMBREE_MANAGE_URL%/}/manage/$(gated_channel_for stable)/${comp} (row ${CUT_ROW})"
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
    register)   register_only "${DIST_COMP}" "${DIST_STAMP}" ;;
    beta)       publish_beta "${DIST_COMP}" "${DIST_STAMP}" ;;
esac
