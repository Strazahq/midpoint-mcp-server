#!/bin/sh
# changelog-section.sh prints the CHANGELOG.md section for one release.
#
# Usage: changelog-section.sh vX.Y.Z [path/to/CHANGELOG.md]
#
# It prints the text under the heading "## [X.Y.Z]" up to the next "## "
# heading, without leading or trailing blank lines. It exits 1 with a message
# on stderr when the tag is not vX.Y.Z, when the file has no such heading, or
# when the section is empty. The release workflow uses it both as a gate and
# as the source of the release notes.
set -eu

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: changelog-section.sh vX.Y.Z [path/to/CHANGELOG.md]" >&2
  exit 2
fi

tag=$1
file=${2:-CHANGELOG.md}
version=${tag#v}

# In GitHub Actions an ::error:: line also shows on the run's summary page.
if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
  prefix="::error::"
else
  prefix="error: "
fi

fail() {
  echo "${prefix}$1" >&2
  exit 1
}

if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  fail "Release refused: the tag '$tag' is not of the form vX.Y.Z. This workflow publishes final releases only. Delete the tag and push one such as v1.2.3."
fi

if [ ! -f "$file" ]; then
  fail "Release refused: $file was not found. The release notes come from it. Run this from the repository root or pass the path as the second argument."
fi

status=0
awk -v head="## [$version]" '
  !found {
    if (index($0, head) == 1) {
      rest = substr($0, length(head) + 1)
      if (rest == "" || rest ~ /^[ \t]/) found = 1
    }
    next
  }
  /^## / { exit }
  {
    if (n == 0 && $0 ~ /^[ \t]*$/) next
    line[++n] = $0
    if ($0 !~ /^[ \t]*$/) last = n
  }
  END {
    if (!found) exit 3
    if (last == 0) exit 4
    for (i = 1; i <= last; i++) print line[i]
  }
' "$file" || status=$?

case $status in
  0) ;;
  3) fail "Release refused: $file has no heading '## [$version]' for the tag $tag. The release notes come from that section. Add a section headed '## [$version] - YYYY-MM-DD', commit it, then delete the tag and push it again on that commit." ;;
  4) fail "Release refused: the '## [$version]' section in $file is empty. The release notes come from that section. Describe the release under that heading, commit it, then delete the tag and push it again on that commit." ;;
  *) fail "Release refused: reading $file failed (awk exit status $status). Check that the file is readable text." ;;
esac
