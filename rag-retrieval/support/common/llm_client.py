# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""LLM client.

Every pipeline calls ``generate()`` in-process; the rest of the codebase only
depends on this function's signature, never on a specific provider's wire
format. Two providers are supported, selected via ``LLM_PROVIDER`` in
``.env``:

- ``bedrock`` (default): Amazon Bedrock, via the Anthropic Messages API
  request/response shape (what Bedrock expects for Claude models). Auth is
  via standard AWS credential resolution (env vars, shared config/
  credentials file, or an assumed role).
- ``openai``: any OpenAI-compatible ``/chat/completions`` endpoint (OpenAI
  itself, or a self-hosted/alternate provider that speaks the same API),
  for anyone without Bedrock access. Uses the official ``openai`` Python SDK,
  imported only when this provider is selected. The 60-second timeout and
  single-attempt behavior are retained (SDK retries are disabled).
"""
from __future__ import annotations

import json
from functools import lru_cache
from typing import Optional

import boto3

from .config import Settings, load_settings
from .logging import get_logger

LOGGER = get_logger(__name__)


@lru_cache(maxsize=1)
def _client(region: str):
    return boto3.client("bedrock-runtime", region_name=region)


def _generate_bedrock(
    prompt: str,
    *,
    settings: Settings,
    system: Optional[str],
    max_tokens: Optional[int],
    temperature: Optional[float],
) -> str:
    if not settings.bedrock_model_id:
        raise RuntimeError(
            "BEDROCK_MODEL_ID is not set. Add it to your .env, e.g. "
            "BEDROCK_MODEL_ID=anthropic.claude-3-5-sonnet-20241022-v2:0"
        )

    body = {
        "anthropic_version": "bedrock-2023-05-31",
        "max_tokens": int(max_tokens if max_tokens is not None else settings.bedrock_max_tokens),
        "temperature": float(temperature if temperature is not None else settings.bedrock_temperature),
        "messages": [{"role": "user", "content": prompt}],
    }
    if system:
        body["system"] = system

    client = _client(settings.aws_region)
    LOGGER.info("Invoking Bedrock model '%s' in %s", settings.bedrock_model_id, settings.aws_region)
    response = client.invoke_model(modelId=settings.bedrock_model_id, body=json.dumps(body))
    payload = json.loads(response["body"].read())

    content = payload.get("content", [])
    if content and isinstance(content, list):
        return "".join(part.get("text", "") for part in content if part.get("type") == "text").strip()
    return str(payload).strip()


def _generate_openai(
    prompt: str,
    *,
    settings: Settings,
    system: Optional[str],
    max_tokens: Optional[int],
    temperature: Optional[float],
) -> str:
    if not settings.openai_api_key:
        raise RuntimeError("OPENAI_API_KEY is not set. Add it to your .env.")
    if not settings.openai_model:
        raise RuntimeError(
            "OPENAI_MODEL is not set. Add it to your .env, e.g. OPENAI_MODEL=gpt-4o-mini"
        )

    messages = []
    if system:
        messages.append({"role": "system", "content": system})
    messages.append({"role": "user", "content": prompt})

    body = {
        "model": settings.openai_model,
        "messages": messages,
        "max_completion_tokens": int(max_tokens if max_tokens is not None else settings.bedrock_max_tokens),
    }
    # GPT-6 defaults to reasoning, which does not accept temperature. Other
    # OpenAI-compatible models retain the lab's existing sampling setting.
    if not settings.openai_model.startswith("gpt-6"):
        body["temperature"] = float(
            temperature if temperature is not None else settings.bedrock_temperature
        )

    # Keep the SDK optional at import time for callers using only Bedrock.
    from openai import OpenAI

    LOGGER.info("Invoking OpenAI-compatible model '%s'", settings.openai_model)
    # Preserve the existing chat-completions payload, including max_completion_tokens,
    # for compatible endpoints. Do not silently retry a billable lab request.
    with OpenAI(
        base_url=settings.openai_base_url,
        api_key=settings.openai_api_key,
        timeout=60.0,
        max_retries=0,
    ) as client:
        response = client.chat.completions.create(**body)

    if not response.choices:
        raise RuntimeError("The OpenAI-compatible endpoint returned no completion choices.")
    message = response.choices[0].message
    if message.content is None:
        refusal = getattr(message, "refusal", None)
        if refusal:
            return refusal.strip()
        raise RuntimeError("The OpenAI-compatible endpoint returned no text content.")
    return message.content.strip()


def generate(
    prompt: str,
    *,
    settings: Optional[Settings] = None,
    system: Optional[str] = None,
    max_tokens: Optional[int] = None,
    temperature: Optional[float] = None,
) -> str:
    """Generate one completion from the configured LLM provider.

    Dispatches to Bedrock or an OpenAI-compatible endpoint based on
    ``settings.llm_provider`` -- see module docstring.
    """
    settings = settings or load_settings()

    if settings.llm_provider == "openai":
        return _generate_openai(
            prompt, settings=settings, system=system, max_tokens=max_tokens, temperature=temperature
        )
    if settings.llm_provider == "bedrock":
        return _generate_bedrock(
            prompt, settings=settings, system=system, max_tokens=max_tokens, temperature=temperature
        )
    raise RuntimeError(
        f"LLM_PROVIDER={settings.llm_provider!r} is not recognized. Set it to 'bedrock' or 'openai' in .env."
    )


__all__ = ["generate"]
