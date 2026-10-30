# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0

PYTHON ?= python
ARGS ?=

.PHONY: install monitor policy validate clean

install:
	$(PYTHON) -m pip install -r ../../support/requirements.txt

monitor:
	$(PYTHON) -m src.monitor $(ARGS)

policy:
	$(PYTHON) -m src.policy_registry $(ARGS)

validate:
	$(PYTHON) -m src.validate

clean:
	find . -type d -name __pycache__ -prune -exec rm -rf {} +
	rm -rf .pytest_cache
