#!/usr/bin/env python3
"""Extract OpenClaw model catalog to models.csv.

Parses TypeScript source files from the openclaw-github repo and produces
a CSV with one row per model, suitable for auditing and catalog tracking.

Usage:
    python3 scripts/extract_models.py
    # or via make:
    make extract-models
"""

import csv
import re
from pathlib import Path

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------

OPENCLAW_SRC = Path("~/openclaw-github/src").expanduser()
OUTPUT_CSV = Path(__file__).parent.parent / "models.csv"

FIELDNAMES = [
    "provider_key",
    "provider_label",
    "icon_key",
    "api_format",
    "base_url",
    "model_id",
    "model_name",
    "reasoning",
    "vision",
    "context_window",
    "max_tokens",
]

# ---------------------------------------------------------------------------
# Text utilities
# ---------------------------------------------------------------------------


def strip_comments(text: str) -> str:
    """Remove // and /* */ comments from TypeScript, respecting string literals."""
    result: list[str] = []
    i = 0
    n = len(text)
    while i < n:
        c = text[i]
        # String literal — preserve as-is (don't strip // inside strings)
        if c in ('"', "'", "`"):
            q = c
            result.append(c)
            i += 1
            while i < n:
                ch = text[i]
                result.append(ch)
                if ch == "\\" and i + 1 < n:
                    i += 1
                    result.append(text[i])
                elif ch == q:
                    break
                i += 1
            i += 1
        # Block comment /* ... */
        elif c == "/" and i + 1 < n and text[i + 1] == "*":
            i += 2
            while i + 1 < n and not (text[i] == "*" and text[i + 1] == "/"):
                i += 1
            i += 2
            result.append(" ")
        # Line comment // ...
        elif c == "/" and i + 1 < n and text[i + 1] == "/":
            while i < n and text[i] != "\n":
                i += 1
        else:
            result.append(c)
            i += 1
    return "".join(result)


def balanced_extract(text: str, start: int, open_c: str, close_c: str) -> str | None:
    """Return text[start..matching_close_c] or None if unbalanced."""
    if start >= len(text) or text[start] != open_c:
        return None
    depth = 0
    in_str = False
    str_char = ""
    i = start
    while i < len(text):
        c = text[i]
        if in_str:
            if c == "\\":
                i += 2
                continue
            if c == str_char:
                in_str = False
        else:
            if c in ('"', "'", "`"):
                in_str = True
                str_char = c
            elif c == open_c:
                depth += 1
            elif c == close_c:
                depth -= 1
                if depth == 0:
                    return text[start : i + 1]
        i += 1
    return None


# ---------------------------------------------------------------------------
# Constant extraction
# ---------------------------------------------------------------------------


def extract_string_consts(text: str) -> dict[str, str]:
    """Extract: [export] const NAME = "value" | 'value' | `value`"""
    result: dict[str, str] = {}
    pat = re.compile(
        r"(?:export\s+)?const\s+([A-Z_][A-Z0-9_]*)\s*=\s*"
        r'(?:"([^"\\]*(?:\\.[^"\\]*)*)"|\'([^\'\\]*(?:\\.[^\'\\]*)*)\'|`([^`\\]*(?:\\.[^`\\]*)*)`)'
    )
    for m in pat.finditer(text):
        result[m.group(1)] = m.group(2) or m.group(3) or m.group(4) or ""
    return result


def extract_numeric_consts(text: str) -> dict[str, int]:
    """Extract: [export] const NAME = 12345"""
    result: dict[str, int] = {}
    for m in re.finditer(
        r"(?:export\s+)?const\s+([A-Z_][A-Z0-9_]*)\s*=\s*(\d+)", text
    ):
        result[m.group(1)] = int(m.group(2))
    return result


# ---------------------------------------------------------------------------
# Object parsing
# ---------------------------------------------------------------------------


def parse_model_object(
    block: str,
    str_c: dict[str, str],
    num_c: dict[str, int],
    func_name: str | None = None,
) -> dict | None:
    """
    Parse a {...} model object block.
    Returns a dict with model fields, or None if not a model object.
    func_name is the wrapping function call (e.g. 'buildMinimaxTextModel'), if any.
    """
    # id: "string" | CONSTANT
    id_m = re.search(
        r"""\bid\s*:\s*(?:"([^"\\]*(?:\\.[^"\\]*)*)"|'([^'\\]*(?:\\.[^'\\]*)*)'|`([^`\\]*(?:\\.[^`\\]*)*)`|([A-Z_][A-Z0-9_]*))""",
        block,
    )
    if not id_m:
        return None
    if id_m.group(4):
        model_id = str_c.get(id_m.group(4), "")
    else:
        model_id = id_m.group(1) or id_m.group(2) or id_m.group(3) or ""
    if not model_id:
        return None

    # name: "string" | CONSTANT
    name_m = re.search(
        r"""\bname\s*:\s*(?:"([^"\\]*(?:\\.[^"\\]*)*)"|'([^'\\]*(?:\\.[^'\\]*)*)'|`([^`\\]*(?:\\.[^`\\]*)*)`|([A-Z_][A-Z0-9_]*))""",
        block,
    )
    if not name_m:
        return None
    if name_m.group(4):
        model_name = str_c.get(name_m.group(4), "")
    else:
        model_name = name_m.group(1) or name_m.group(2) or name_m.group(3) or ""
    if not model_name:
        return None

    # reasoning: true | false
    r_m = re.search(r"\breasoning\s*:\s*(true|false)", block)
    reasoning = r_m.group(1) if r_m else "false"

    # vision: derived from input containing "image"
    vision = '"image"' in block or "'image'" in block
    # buildMinimaxTextModel always uses input: ["text"]
    if func_name == "buildMinimaxTextModel":
        vision = False

    # contextWindow: integer literal or named constant
    ctx_m = re.search(r"\bcontextWindow\s*:\s*(\d+|[A-Z_][A-Z0-9_]*)", block)
    context_window = ""
    if ctx_m:
        v = ctx_m.group(1)
        if v.isdigit():
            context_window = v
        else:
            nv = num_c.get(v)
            if nv is not None:
                context_window = str(nv)

    # maxTokens: integer literal or named constant
    max_m = re.search(r"\bmaxTokens\s*:\s*(\d+|[A-Z_][A-Z0-9_]*)", block)
    max_tokens = ""
    if max_m:
        v = max_m.group(1)
        if v.isdigit():
            max_tokens = v
        else:
            nv = num_c.get(v)
            if nv is not None:
                max_tokens = str(nv)

    # For minimax helper functions, apply defaults when fields are absent
    if func_name in ("buildMinimaxTextModel", "buildMinimaxModel"):
        if not context_window:
            context_window = str(num_c.get("MINIMAX_DEFAULT_CONTEXT_WINDOW", 200000))
        if not max_tokens:
            max_tokens = str(num_c.get("MINIMAX_DEFAULT_MAX_TOKENS", 8192))

    return {
        "model_id": model_id,
        "model_name": model_name,
        "reasoning": reasoning,
        "vision": "true" if vision else "false",
        "context_window": context_window,
        "max_tokens": max_tokens,
    }


# ---------------------------------------------------------------------------
# Array scanner
# ---------------------------------------------------------------------------


def scan_array_for_models(
    array_content: str,
    str_c: dict[str, str],
    num_c: dict[str, int],
    volc_objects: dict[str, dict] | None = None,
) -> list[dict]:
    """
    Scan [...] array content in source order.
    Handles:
      - Inline object literals  { id: ..., name: ..., ... }
      - Function-call objects   buildHelper({ id: ..., name: ..., ... })
      - Bare volc refs          VOLC_MODEL_KIMI_K2_5
    """
    if volc_objects is None:
        volc_objects = {}
    results: list[dict] = []
    inner = array_content[1:-1]  # strip outer [ ]
    i = 0
    n = len(inner)
    # bracket_depth tracks ( and [ nesting (not {, which is handled by balanced_extract)
    bracket_depth = 0

    while i < n:
        c = inner[i]

        if c in ("(", "["):
            bracket_depth += 1
            i += 1

        elif c in (")", "]"):
            bracket_depth -= 1
            i += 1

        elif c in ('"', "'", "`"):
            # Skip string literal
            q = c
            i += 1
            while i < n:
                if inner[i] == "\\":
                    i += 2
                elif inner[i] == q:
                    i += 1
                    break
                else:
                    i += 1

        elif c == "{":
            block = balanced_extract(inner, i, "{", "}")
            if block:
                # Check for enclosing function call name (e.g. buildMinimaxTextModel)
                before = inner[:i].rstrip()
                fm = re.search(r"(\w+)\s*\(\s*$", before)
                func_name = fm.group(1) if fm else None
                model = parse_model_object(block, str_c, num_c, func_name)
                if model:
                    results.append(model)
                i += len(block)
            else:
                i += 1

        elif bracket_depth == 0:
            # Look for bare uppercase identifier (volc-style bare ref)
            m = re.match(r"([A-Z][A-Z0-9_]{2,})", inner[i:])
            if m:
                ident = m.group(1)
                if ident in volc_objects:
                    results.append(volc_objects[ident])
                i += len(ident)
            else:
                i += 1

        else:
            i += 1

    return results


# ---------------------------------------------------------------------------
# Locating arrays and function bodies
# ---------------------------------------------------------------------------


def get_array_content(text: str, array_name: str) -> str | None:
    """Find ARRAY_NAME = [...] and return the [...] block."""
    m = re.search(rf"\b{re.escape(array_name)}\b[^=]*=\s*\[", text)
    if not m:
        return None
    # \[ is the last char of the match
    bracket_pos = m.end() - 1
    return balanced_extract(text, bracket_pos, "[", "]")


def get_function_body(text: str, func_name: str) -> str | None:
    """Find `function funcName(...)...{` and return its body {...}."""
    m = re.search(
        rf"(?:export\s+)?(?:async\s+)?function\s+{re.escape(func_name)}\s*\(",
        text,
    )
    if not m:
        return None
    # position of '(' is m.end() - 1
    paren_pos = m.end() - 1
    paren_block = balanced_extract(text, paren_pos, "(", ")")
    if not paren_block:
        return None
    after_params = paren_pos + len(paren_block)
    brace_pos = text.find("{", after_params)
    if brace_pos < 0:
        return None
    return balanced_extract(text, brace_pos, "{", "}")


def get_models_array_from_function(text: str, func_name: str) -> str | None:
    """Find buildXxxProvider() body and return its models: [...] array."""
    body = get_function_body(text, func_name)
    if not body:
        return None
    m = re.search(r"\bmodels\s*:\s*\[", body)
    if not m:
        return None
    bracket_pos = m.end() - 1
    return balanced_extract(body, bracket_pos, "[", "]")


# ---------------------------------------------------------------------------
# Volc shared objects pre-parser
# ---------------------------------------------------------------------------


def parse_volc_shared(path: Path) -> dict[str, dict]:
    """Parse VOLC_MODEL_KIMI_K2_5 and VOLC_MODEL_GLM_4_7 from volc-models.shared.ts."""
    text = strip_comments(path.read_text())
    str_c = extract_string_consts(text)
    num_c = extract_numeric_consts(text)
    objects: dict[str, dict] = {}
    for name in ("VOLC_MODEL_KIMI_K2_5", "VOLC_MODEL_GLM_4_7"):
        m = re.search(rf"(?:export\s+)?const\s+{re.escape(name)}\s*=\s*\{{", text)
        if not m:
            continue
        brace_pos = text.index("{", m.start())
        block = balanced_extract(text, brace_pos, "{", "}")
        if not block:
            continue
        model = parse_model_object(block, str_c, num_c)
        if model:
            objects[name] = model
    return objects


# ---------------------------------------------------------------------------
# Provider definitions
# ---------------------------------------------------------------------------

PROVIDERS = [
    {
        "key": "together",
        "label": "Together AI",
        "icon_key": "togetherai",
        "api_format": "openai-completions",
        "base_url": "https://api.together.xyz/v1",
        "type": "file",
        "file": "agents/together-models.ts",
        "catalog_var": "TOGETHER_MODEL_CATALOG",
    },
    {
        "key": "venice",
        "label": "Venice AI",
        "icon_key": "",
        "api_format": "openai-completions",
        "base_url": "https://api.venice.ai/api/v1",
        "type": "file",
        "file": "agents/venice-models.ts",
        "catalog_var": "VENICE_MODEL_CATALOG",
    },
    {
        "key": "synthetic",
        "label": "Synthetic",
        "icon_key": "",
        "api_format": "anthropic-messages",
        "base_url": "https://api.synthetic.new/anthropic",
        "type": "file",
        "file": "agents/synthetic-models.ts",
        "catalog_var": "SYNTHETIC_MODEL_CATALOG",
    },
    {
        "key": "kilocode",
        "label": "Kilocode",
        "icon_key": "",
        "api_format": "openai-completions",
        "base_url": "https://api.kilo.ai/api/gateway/",
        "type": "file",
        "file": "providers/kilocode-shared.ts",
        "catalog_var": "KILOCODE_MODEL_CATALOG",
    },
    {
        "key": "huggingface",
        "label": "Hugging Face",
        "icon_key": "huggingface",
        "api_format": "openai-completions",
        "base_url": "https://router.huggingface.co/v1",
        "type": "file",
        "file": "agents/huggingface-models.ts",
        "catalog_var": "HUGGINGFACE_MODEL_CATALOG",
    },
    {
        "key": "volcengine",
        "label": "Volcano Engine (Doubao)",
        "icon_key": "volcengine",
        "api_format": "openai-completions",
        "base_url": "https://ark.cn-beijing.volces.com/api/v3",
        "type": "file",
        "file": "agents/doubao-models.ts",
        "catalog_var": "DOUBAO_MODEL_CATALOG",
        "volc_refs": True,
    },
    {
        "key": "byteplus",
        "label": "BytePlus",
        "icon_key": "bytedance",
        "api_format": "openai-completions",
        "base_url": "https://ark.ap-southeast.bytepluses.com/api/v3",
        "type": "file",
        "file": "agents/byteplus-models.ts",
        "catalog_var": "BYTEPLUS_MODEL_CATALOG",
        "volc_refs": True,
    },
    {
        "key": "minimax",
        "label": "MiniMax",
        "icon_key": "minimax",
        "api_format": "anthropic-messages",
        "base_url": "https://api.minimax.io/anthropic",
        "type": "func",
        "func": "buildMinimaxProvider",
    },
    {
        "key": "moonshot",
        "label": "Moonshot (Kimi)",
        "icon_key": "moonshot",
        "api_format": "openai-completions",
        "base_url": "https://api.moonshot.ai/v1",
        "type": "func",
        "func": "buildMoonshotProvider",
    },
    {
        "key": "kimi-coding",
        "label": "Kimi Coding",
        "icon_key": "moonshot",
        "api_format": "anthropic-messages",
        "base_url": "https://api.kimi.com/coding/",
        "type": "func",
        "func": "buildKimiCodingProvider",
    },
    {
        "key": "qwen-portal",
        "label": "Qwen Portal",
        "icon_key": "qwen",
        "api_format": "openai-completions",
        "base_url": "https://portal.qwen.ai/v1",
        "type": "func",
        "func": "buildQwenPortalProvider",
    },
    {
        "key": "openrouter",
        "label": "OpenRouter",
        "icon_key": "openrouter",
        "api_format": "openai-completions",
        "base_url": "https://openrouter.ai/api/v1",
        "type": "func",
        "func": "buildOpenrouterProvider",
    },
    {
        "key": "qianfan",
        "label": "Qianfan (Baidu)",
        "icon_key": "wenxin",
        "api_format": "openai-completions",
        "base_url": "https://qianfan.baidubce.com/v2",
        "type": "func",
        "func": "buildQianfanProvider",
    },
    {
        "key": "nvidia",
        "label": "NVIDIA",
        "icon_key": "nvidia",
        "api_format": "openai-completions",
        "base_url": "https://integrate.api.nvidia.com/v1",
        "type": "func",
        "func": "buildNvidiaProvider",
    },
    {
        "key": "xiaomi",
        "label": "Xiaomi MiMo",
        "icon_key": "xiaomimimo",
        "api_format": "anthropic-messages",
        "base_url": "https://api.xiaomimimo.com/anthropic",
        "type": "func",
        "func": "buildXiaomiProvider",
    },
    {
        "key": "openai",
        "label": "OpenAI",
        "icon_key": "openai",
        "api_format": "openai-responses",
        "base_url": "https://api.openai.com/v1",
        "type": "hardcoded",
        "models": [
            {
                "model_id": "gpt-5.2",
                "model_name": "GPT-5.2",
                "reasoning": "true",
                "vision": "true",
                "context_window": "400000",
                "max_tokens": "128000",
            },
            {
                "model_id": "gpt-5.1",
                "model_name": "GPT-5.1",
                "reasoning": "true",
                "vision": "true",
                "context_window": "400000",
                "max_tokens": "128000",
            },
        ],
    },
    {
        "key": "openai-codex",
        "label": "OpenAI Codex",
        "icon_key": "openai",
        "api_format": "openai-codex-responses",
        "base_url": "https://api.openai.com/v1",
        "type": "hardcoded",
        "models": [
            {
                "model_id": "gpt-5.1-codex",
                "model_name": "GPT-5.1 Codex",
                "reasoning": "true",
                "vision": "true",
                "context_window": "400000",
                "max_tokens": "128000",
            },
            {
                "model_id": "gpt-5.1-codex-mini",
                "model_name": "GPT-5.1 Codex Mini",
                "reasoning": "true",
                "vision": "true",
                "context_window": "400000",
                "max_tokens": "128000",
            },
            {
                "model_id": "gpt-5.1-codex-max",
                "model_name": "GPT-5.1 Codex Max",
                "reasoning": "true",
                "vision": "true",
                "context_window": "400000",
                "max_tokens": "128000",
            },
        ],
    },
    {
        "key": "anthropic",
        "label": "Anthropic",
        "icon_key": "anthropic",
        "api_format": "anthropic-messages",
        "base_url": "https://api.anthropic.com",
        "type": "hardcoded",
        "models": [
            {
                "model_id": "claude-opus-4-6",
                "model_name": "Claude Opus 4.6",
                "reasoning": "true",
                "vision": "true",
                "context_window": "1000000",
                "max_tokens": "128000",
            },
            {
                "model_id": "claude-opus-4-5",
                "model_name": "Claude Opus 4.5",
                "reasoning": "true",
                "vision": "true",
                "context_window": "200000",
                "max_tokens": "64000",
            },
        ],
    },
]


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main() -> None:
    # Pre-parse volc shared objects (used as bare refs in doubao/byteplus catalogs)
    volc_path = OPENCLAW_SRC / "agents/volc-models.shared.ts"
    volc_objects = parse_volc_shared(volc_path)
    print(f"Pre-parsed {len(volc_objects)} volc shared objects: {list(volc_objects.keys())}")

    # Pre-parse models-config.providers.ts (used for all inline `type: func` providers)
    providers_path = OPENCLAW_SRC / "agents/models-config.providers.ts"
    providers_text = strip_comments(providers_path.read_text())
    providers_str_c = extract_string_consts(providers_text)
    providers_num_c = extract_numeric_consts(providers_text)

    rows: list[dict] = []

    for prov in PROVIDERS:
        key = prov["key"]
        ptype = prov["type"]
        print(f"Processing {key} ({ptype})...", end="")

        base_row = {
            "provider_key": key,
            "provider_label": prov["label"],
            "icon_key": prov.get("icon_key", ""),
            "api_format": prov["api_format"],
            "base_url": prov["base_url"],
        }

        if ptype == "hardcoded":
            for m in prov["models"]:
                rows.append({**base_row, **m})
            print(f" {len(prov['models'])} models")

        elif ptype == "file":
            path = OPENCLAW_SRC / prov["file"]
            text = strip_comments(path.read_text())
            str_c = extract_string_consts(text)
            num_c = extract_numeric_consts(text)
            use_volc = prov.get("volc_refs", False)
            array_content = get_array_content(text, prov["catalog_var"])
            if not array_content:
                print(f"\n  WARNING: Could not find {prov['catalog_var']} in {prov['file']}")
                continue
            models = scan_array_for_models(
                array_content, str_c, num_c, volc_objects if use_volc else None
            )
            print(f" {len(models)} models")
            for m in models:
                rows.append({**base_row, **m})

        elif ptype == "func":
            func_name = prov["func"]
            array_content = get_models_array_from_function(providers_text, func_name)
            if not array_content:
                print(f"\n  WARNING: Could not find models array in {func_name}()")
                continue
            models = scan_array_for_models(array_content, providers_str_c, providers_num_c)
            print(f" {len(models)} models")
            for m in models:
                rows.append({**base_row, **m})

    # Write CSV
    with open(OUTPUT_CSV, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=FIELDNAMES)
        writer.writeheader()
        writer.writerows(rows)

    print(f"\nWrote {len(rows)} rows to {OUTPUT_CSV}")


if __name__ == "__main__":
    main()
