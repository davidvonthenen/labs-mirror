#!/usr/bin/env bash
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

readonly SOURCE_REPO=/opt/lab/repository
readonly TARGET_REPO="${LAB_REPO_DIR:-/workspace/rag-retrieval}"
mkdir -p "$TARGET_REPO"

if [[ ! -f "$TARGET_REPO/.lab-revision" ]]; then
    # Do not merge a cloned repository into arbitrary existing participant files.
    if [[ -n "$(find "$TARGET_REPO" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
        echo "ERROR: Workspace is nonempty but is not a seeded workspace: $TARGET_REPO" >&2
        echo "Preserve its contents before choosing an empty workspace volume." >&2
        exit 1
    fi
    cp -R "$SOURCE_REPO/." "$TARGET_REPO/"
    cp /opt/lab/repository-revision.txt "$TARGET_REPO/.lab-revision"
    echo "Initialized the persistent lab workspace from the image's stripped clone."
fi

for component in baseline-bm25-rag baseline-vector-rag governance-tiering \
                 hybrid-rag-native hybrid-rag-ner-rerank operationalize-production; do
    if [[ ! -f "$TARGET_REPO/scripts/$component/Makefile" ]]; then
        echo "ERROR: Missing scripts/$component/Makefile in $TARGET_REPO" >&2
        exit 1
    fi
done

current="$(cat "$TARGET_REPO/.lab-revision" 2>/dev/null || echo 'none')"
baked="$(cat /opt/lab/repository-revision.txt)"
if [[ "$current" != "$baked" ]]; then
    echo "NOTICE: Workspace commit differs from the image's clone; preserving your workspace."
fi

echo "Workspace: $TARGET_REPO"
echo "Python: $(python --version)"
echo "Open a terminal with: podman compose exec lab bash"
echo "NER is started manually: cd scripts/baseline-bm25-rag && make ner"
echo "No ingestion, queries, OpenSearch or LLM service is started automatically."
exec "$@"
