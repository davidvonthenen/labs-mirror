# RAG Retrieval integration validation

Date: 2026-10-29.

The integration combines the unchanged application source from
`instaclustr/labs` commit `c321a65c9586706d8696645327e28b07f6b78b77` with the
supplied agent-documentation additions and the Go CLI's `insta.yaml` contract.
Existing source paths, executable modules, and internal `common` symlinks are
preserved. The overlay changes documentation and adds CLI metadata; it does not
replace the Python retrieval application.

## Checks and boundaries

- The Go templates tests validate the manifest against real source paths and
  declared Python entry points, preserve application bytes and symlink targets,
  and reject edited or unsafe overlay destinations.
- Both bundled lab manifests pass the checked-in JSON Schema using a Draft
  2020-12 validator. The Go manifest loader separately checks references,
  configuration keys, operation names, service dependencies, and project paths.
- The original `scripts/validate_lab.py` checks this composed source tree's
  metadata, document links, task graph, command sources, Skill metadata,
  teaching structure, and Python syntax. Its latest result is recorded in the
  CLI package's implementation report.
- The supplied ZIP omits the `tests/` directory and
  `tests/test_validate_lab.py`; that regression suite was not run. References to
  that absent linked file were corrected without inventing missing tests.
- Cloud provisioning, data-plane authentication, NER model installation, paid
  inference, and live educational assertions require their actual services and
  credentials. Structural and fixture checks do not establish those outcomes.

Run `python scripts/validate_lab.py` from this directory after installing the
small `support/requirements-test.txt` dependency set when repeating the local
checker manually. The Go CLI automatically manages the application runtime for
normal `insta` operations; manual Python activation is unnecessary on that path.
