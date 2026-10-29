#!/bin/sh
# Regenerates internal/graphql/generated.go. Takes a lock so that two
# concurrent runs do not write the file at the same time.
set -e
cd "$(dirname "$0")/.."
lock=.generate.lock
i=0
until mkdir "$lock" 2>/dev/null; do
	i=$((i + 1))
	if [ "$i" -gt 300 ]; then
		echo "generate.sh: gave up waiting for $lock" >&2
		exit 1
	fi
	sleep 1
done
trap 'rmdir "$lock"' EXIT
go tool genqlient "$@"
