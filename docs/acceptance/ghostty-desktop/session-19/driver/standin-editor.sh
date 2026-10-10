#!/bin/sh
echo "EDITOR VIEW (stand-in editor) in $(pwd)"
echo "editing: $*"
printf 'press enter to close the editor: '
read -r _
