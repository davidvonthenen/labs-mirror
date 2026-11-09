# Install an insta release binary

Choose the release archive for your operating system and processor:
`darwin_arm64` for Apple Silicon, `darwin_amd64` for Intel macOS,
`linux_arm64` for ARM64 Linux, or `linux_amd64` for AMD64 Linux.
Verify the downloaded archive against the release's `SHA256SUMS` file, then
extract it. From the extracted directory:

```bash
mkdir -p "$HOME/.local/bin"
cp insta "$HOME/.local/bin/insta"
chmod u+x "$HOME/.local/bin/insta"
export PATH="$HOME/.local/bin:$PATH"
insta version
```

Python 3.12 with `venv` is required for these labs. Go and Git are not required
to use the binary. Initial project setup downloads the lab's Python packages
and declared models. The binaries are unsigned.

```bash
insta lab list
insta new my-rag --lab rag-retrieval
cd my-rag

export INSTACLUSTR_PROVISION_USERNAME='your-instaclustr-username'
export INSTACLUSTR_PROVISION_API_KEY='your-provisioning-api-key'
export OPENAI_API_KEY='your-application-key'

insta config
insta up
insta exec
```

`up` asks before creating billable infrastructure and opening the requested
`OPENSEARCH` and `OPENSEARCH_DASHBOARDS` rules to `0.0.0.0/0`. This permits all IPv4
source addresses and is intended for disposable labs, not production. The CLI retains a created cluster's
ownership record if connection or setup fails; rerun `up` to reconcile or `down`
to clean it up.

Use `insta exec` for a conversation, `insta exec -p "your question"` for a single
reply, and `insta exec -i` for the native terminal. These modes use an installed,
authenticated Codex CLI. Use `insta exec -i --approve` to start a network-enabled,
workspace-write Codex session with the current project's declared task effects
preapproved. You can also run the lab directly from your shell.

```bash
insta task run baseline-vector-rag ingest
insta task run baseline-vector-rag query
insta task run baseline-vector-rag validate
insta down
```

`new` creates and manages the Python virtual environment. No manual activation,
project pip command, or credential-filled `.env` file is needed.

See the project's generated `INSTA.md`, `insta.yaml`, and task documents, plus
the source package's `README.md` and `VALIDATION.md`, for configuration, recovery,
the optional limited API assistant, and test scope.
