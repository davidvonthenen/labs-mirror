# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""LLM client.

Every pipeline calls ``generate()`` in-process; the rest of the codebase only
depends on this function's signature, never on a specific provider's wire
format. ``LLM_PROVIDER`` selects ``openai`` (default), ``anthropic`` or
``bedrock``. OpenAI uses Chat Completions; Anthropic and AnthropicBedrock use
Messages. SDKs are imported only for the selected provider. Each request uses
a 60-second timeout with SDK retries disabled.
"""
from __future__ import annotations

import os
from typing import Optional

from .config import Settings, _clear_blank_aws_profiles, load_settings
from .logging import get_logger

LOGGER = get_logger(__name__)


def _generate_anthropic(
    prompt: str,
    *,
    settings: Settings,
    system: Optional[str],
    max_tokens: Optional[int],
) -> str:
    use_bedrock = settings.llm_provider == "bedrock"
    if use_bedrock:
        if not settings.bedrock_model_id:
            raise RuntimeError("BEDROCK_MODEL_ID is not set; select a Claude model or inference profile in your AWS account.")
        if not settings.aws_region:
            raise RuntimeError("AWS_REGION is not set.")
        model = settings.bedrock_model_id
    else:
        if not settings.anthropic_api_key:
            raise RuntimeError("ANTHROPIC_API_KEY is not set.")
        if not settings.anthropic_model:
            raise RuntimeError("ANTHROPIC_MODEL is not set; select a model available to your Anthropic account.")
        model = settings.anthropic_model

    body = {
        "model": model,
        "max_tokens": int(max_tokens if max_tokens is not None else settings.bedrock_max_tokens),
        "messages": [{"role": "user", "content": prompt}],
    }
    if system:
        body["system"] = system

    from anthropic import Anthropic, AnthropicBedrock

    if use_bedrock:
        # Let the SDK resolve and refresh AWS credentials; do not cache static keys.
        _clear_blank_aws_profiles()
        client = AnthropicBedrock(
            aws_region=settings.aws_region,
            aws_profile=settings.aws_profile or None,
            timeout=60.0,
            max_retries=0,
        )
    else:
        client = Anthropic(
            api_key=settings.anthropic_api_key,
            # A coding session may explicitly clear this reserved environment key.
            base_url=os.getenv("ANTHROPIC_BASE_URL") or "https://api.anthropic.com",
            timeout=60.0,
            max_retries=0,
        )
    LOGGER.info("Invoking %s model '%s'", settings.llm_provider, model)
    with client:
        response = client.messages.create(**body)
    text = "".join(part.text for part in response.content if part.type == "text").strip()
    if not text:
        raise RuntimeError("The selected Anthropic endpoint returned no text content.")
    return text


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
            "OPENAI_MODEL is not set; select a model available to your OpenAI-compatible endpoint."
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

    # Keep SDK imports local to the selected provider.
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
        if refusal and refusal.strip():
            return refusal.strip()
        raise RuntimeError("The OpenAI-compatible endpoint returned no text content.")
    answer = message.content.strip()
    if not answer:
        raise RuntimeError(
            "The model returned no answer text. Check its output-token budget and response status."
        )
    return answer


def generate(
    prompt: str,
    *,
    settings: Optional[Settings] = None,
    system: Optional[str] = None,
    max_tokens: Optional[int] = None,
) -> str:
    """Generate one completion from the configured LLM provider.

    Dispatches to OpenAI-compatible, Anthropic, or AnthropicBedrock clients based on
    ``settings.llm_provider`` -- see module docstring.
    """
    settings = settings or load_settings()

    if settings.llm_provider == "openai":
        return _generate_openai(
            prompt, settings=settings, system=system, max_tokens=max_tokens
        )
    if settings.llm_provider in ("anthropic", "bedrock"):
        return _generate_anthropic(
            prompt, settings=settings, system=system, max_tokens=max_tokens
        )
    raise RuntimeError(
        f"LLM_PROVIDER={settings.llm_provider!r} is not recognized. Use 'openai', 'anthropic', or 'bedrock'."
    )


__all__ = ["generate"]
