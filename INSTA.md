# insta CLI 0.0.1

This package adds a Go CLI to the Instaclustr labs repository.

- `insta/` contains the complete Go module, tests, embedded lab integration, documentation, prebuilt binaries, and checksums.
- `.github/workflows/insta-release.yml` belongs in the repository root workflow directory.
- Existing `rag-retrieval/` and `agentic-rag/` application folders remain in place.

Start with `insta/README.md`. For an Apple Silicon machine, the ready-to-run binary is `insta/bin/darwin-arm64/insta`. Linux AMD64, Linux ARM64, and Intel macOS binaries are also included.

The standalone RAG manifest is embedded at `insta/internal/templates/overlays/rag-retrieval/insta.yaml`. Its matching deployment definition is in the same directory. New projects receive both files automatically.

