# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0

PYTHON ?= python
QUESTION ?= Suggest a British fish and chips recipe.
ARGS ?=

.PHONY: install ingest query validate clean

install:
	$(PYTHON) -m pip install -r ../../support/requirements.txt

ingest:
	$(PYTHON) -m src.ingest

query:
	$(PYTHON) -m src.query --question "$(QUESTION)" $(ARGS)

validate:
	$(PYTHON) -m src.validate

clean:
	find . -type d -name __pycache__ -prune -exec rm -rf {} +
	rm -rf .pytest_cache
