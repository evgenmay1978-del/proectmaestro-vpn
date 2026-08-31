#!/bin/sh
set -eu

case "$0" in
    */*) SCRIPT_PARENT=${0%/*} ;;
    *) SCRIPT_PARENT=. ;;
esac
SCRIPT_DIR=$(CDPATH= cd -- "$SCRIPT_PARENT" && pwd -P)
exec /usr/bin/python3 "${SCRIPT_DIR}/deploy_node.py" "$@"
