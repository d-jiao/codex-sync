#!/usr/bin/env sh
#
# Print the CHANGELOG section for one version, without its heading.
#
#   scripts/release-notes.sh v0.1.0 [CHANGELOG.md]
#
# Exits non-zero when the version has no section, so a release cannot be
# published for a tag nobody wrote a changelog entry for.

set -eu

if [ $# -lt 1 ]; then
	echo "usage: $0 <version> [changelog]" >&2
	exit 2
fi

version=${1#v}
changelog=${2:-CHANGELOG.md}

if [ ! -f "$changelog" ]; then
	echo "$0: no such file: $changelog" >&2
	exit 2
fi

notes=$(awk -v target="## [$version]" '
	substr($0, 1, length(target)) == target { found = 1; next }
	found && substr($0, 1, 4) == "## [" { exit }
	found { lines[n++] = $0 }
	END {
		first = 0
		while (first < n && lines[first] ~ /^[[:space:]]*$/) first++
		last = n - 1
		while (last >= first && lines[last] ~ /^[[:space:]]*$/) last--
		for (i = first; i <= last; i++) print lines[i]
	}
' "$changelog")

if [ -z "$notes" ]; then
	echo "$0: no CHANGELOG section with content for version $version" >&2
	exit 1
fi

printf '%s\n' "$notes"
