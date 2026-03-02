#!/usr/bin/env bash
# shellcheck shell=bash
# shellcheck disable=SC2034

[[ -n "${_CC_AUDIT_MAIN_LOADED:-}" ]] && return 0
_CC_AUDIT_MAIN_LOADED=1

main() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
        --no-cache)
            opt_no_cache=true
            shift
            ;;
        --skip-ai)
            opt_skip_ai=true
            shift
            ;;
        --section)
            if [[ $# -lt 2 ]]; then
                echo "Error: --section requires a value" >&2
                exit 1
            fi
            opt_section="$2"
            shift 2
            ;;
        --json)
            opt_json=true
            shift
            ;;
        --quiet)
            opt_quiet=true
            shift
            ;;
        --update-manifest)
            "${EDITOR:-vi}" "$MANIFEST"
            exit 0
            ;;
        --help | -h)
            usage
            exit 0
            ;;
        *)
            echo "Error: Unknown option '$1'. Run 'cc-audit --help' for usage." >&2
            exit 1
            ;;
        esac
    done

    validate

    if [[ "$opt_json" == "true" ]]; then
        fetch_docs >&2
    else
        fetch_docs
    fi

    if [[ "$opt_skip_ai" == "false" ]]; then
        if [[ "$opt_json" == "true" ]]; then
            analyze_with_ai >&2
        else
            analyze_with_ai
        fi
    fi

    if [[ "$opt_json" == "true" ]]; then
        report_manifest_json
    else
        report_manifest
    fi

    _update_last_audit
}
