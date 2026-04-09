#!/bin/bash

log_status() { printf '[..] %s\n' "$1"; }
log_success() { printf '[ok] %s\n' "$1"; }
log_error() { printf '[!!] %s\n' "$1" >&2; }
log_warning() { printf '[??] %s\n' "$1"; }
log_verbose() { if [[ "${VERBOSE:-0}" == "1" ]]; then printf '[vv] %s\n' "$1"; fi; }
