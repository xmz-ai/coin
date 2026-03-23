#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <current_version> <major|minor|patch>" >&2
  exit 1
fi

current="$1"
level="$2"

if [[ ! "${current}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid semver: ${current}" >&2
  exit 1
fi

IFS='.' read -r major minor patch <<< "${current}"

case "${level}" in
  major)
    major=$((major + 1))
    minor=0
    patch=0
    ;;
  minor)
    minor=$((minor + 1))
    patch=0
    ;;
  patch)
    patch=$((patch + 1))
    ;;
  *)
    echo "invalid bump level: ${level}" >&2
    exit 1
    ;;
esac

echo "${major}.${minor}.${patch}"
