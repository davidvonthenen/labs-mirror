#!/usr/bin/env python3
# Copyright 2026 NetApp Instaclustr contributors. All Rights Reserved.
# SPDX-License-Identifier: Apache-2.0
"""Check the workshop's agent metadata and teaching documents without running it.

Run from any directory: python /path/to/rag-retrieval/scripts/validate_lab.py
This checker never executes manifest commands, imports the RAG code, or reads .env.
"""

from __future__ import annotations

import ast
import json
import os
from pathlib import Path
import re
import sys
import tokenize
from urllib.parse import unquote, urlsplit


EXCLUDED_DIRS = {
    ".git", ".insta", ".venv", "venv", ".cache", ".pytest_cache", "__pycache__",
    "node_modules", "build", "dist", "artifacts", ".aws",
}
EXECUTABLE_LANGUAGES = {"bash", "sh", "shell", "zsh", "python", "py", "sql", "http", "console"}
AUTHOR_TOKENS = re.compile(r"\{\{[^{}]+\}\}|\bREPLACE_ME\b|\[AUTHOR(?:[: ][^\]]*)?\]")
PROSE_AUTHOR_TOKENS = re.compile(r"\b(?:TODO|TBD|FIXME)\b")


def markdown_parts(text: str) -> tuple[str, list[tuple[int, int, str]]]:
    """Mask fenced code while retaining line positions for headings and links."""
    lines = text.splitlines()
    plain, blocks = list(lines), []
    opening = None
    for i, line in enumerate(lines):
        fence = re.match(r"^\s{0,3}(`{3,}|~{3,})(.*)$", line)
        if opening is None and fence:
            marker, info = fence.groups()
            opening = (i, marker, info.strip().split()[0].lower() if info.strip() else "")
        elif opening and fence and fence[1][0] == opening[1][0] and len(fence[1]) >= len(opening[1]) and not fence[2].strip():
            blocks.append((opening[0], i, opening[2]))
            opening = None
        if opening or fence:
            plain[i] = ""
    if opening:
        blocks.append((opening[0], len(lines), "unclosed"))
    return "\n".join(plain), blocks


def slug(heading: str) -> str:
    heading = re.sub(r"\[([^]]+)\]\([^)]+\)", r"\1", heading)
    heading = re.sub(r"<[^>]+>", "", heading).strip().lower()
    return re.sub(r"[^\w\- ]", "", heading).replace(" ", "-")


def anchors(text: str) -> set[str]:
    plain, _ = markdown_parts(text)
    found = set(re.findall(r"<(?:a|[a-z][a-z0-9]*)\b[^>]*\b(?:id|name)=[\"']([^\"']+)[\"']", plain, re.I))
    for match in re.finditer(r"^ {0,3}#{1,6} +(.+?)(?: +#+)?\s*$", plain, re.M):
        base = slug(match[1])
        candidate, suffix = base, 0
        while candidate in found:
            suffix += 1
            candidate = f"{base}-{suffix}"
        found.add(candidate)
    return found


def markdown_links(text: str) -> list[str]:
    """Read inline/image links and reference definitions, ignoring code examples."""
    plain, _ = markdown_parts(text)
    plain = re.sub(r"(`+).*?\1", "", plain)
    links = re.findall(r"\]\(\s*(<[^>]+>|(?:\\.|[^\s()]|\([^()]*\))+)\s*(?:[\"'][^\n]*?[\"']\s*)?\)", plain)
    links += re.findall(r"^ {0,3}\[[^]\n]+\]:\s*(<[^>]+>|\S+)", plain, re.M)
    links += re.findall(r"<[^>]+\b(?:href|src)=[\"']([^\"']+)[\"']", plain, re.I)
    return [link.strip("<>") for link in links]


class LabValidator:
    def __init__(self, root: Path):
        self.root = root.resolve()
        self.report = {
            "ok": True,
            "checked": [],
            "counts": {"tasks": 0, "markdown_files": 0, "python_files": 0},
            "errors": [],
            "not_checked": ["runtime configuration", "external services", "command execution"],
        }

    def error(self, code: str, path: str | Path, message: str) -> None:
        self.report["ok"] = False
        self.report["errors"].append({"code": code, "path": str(path), "message": message})

    def path(self, value: str, context: str, *, base: Path | None = None) -> Path | None:
        candidate = (base or self.root) / value
        if Path(value).is_absolute():
            self.error("invalid_path", context, f"Expected a relative project path: {value}")
            return None
        try:
            resolved = candidate.resolve(strict=True)
            if not resolved.is_relative_to(self.root):
                self.error("outside_project", context, f"Path or symlink leaves the project: {value}")
                return None
            return resolved
        except (OSError, RuntimeError):
            self.error("missing_path", context, f"Missing path or broken symlink: {value}")
            return None

    def files(self) -> list[Path]:
        files = []
        for directory, names, filenames in os.walk(self.root, followlinks=False):
            names[:] = sorted(name for name in names if name not in EXCLUDED_DIRS)
            for name in names + sorted(filenames):
                path = Path(directory) / name
                if path.is_symlink():
                    self.path(str(path.relative_to(self.root)), str(path.relative_to(self.root)))
                elif path.is_file():
                    files.append(path)
        return files

    def load_json(self, relative: str) -> object | None:
        def unique_object(pairs):
            result = {}
            for key, value in pairs:
                if key in result:
                    raise ValueError(f"Duplicate JSON property: {key}")
                result[key] = value
            return result

        path = self.path(relative, relative)
        if path is None:
            return None
        try:
            return json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=unique_object)
        except (ValueError, OSError, UnicodeError) as exc:
            self.error("invalid_json", relative, str(exc))
            return None

    def check_schema(self, schema_validator) -> dict | None:
        schema = self.load_json("references/lab.schema.json")
        manifest = self.load_json("references/lab.json")
        if schema is None or manifest is None:
            return None
        pending = [schema]
        while pending:
            item = pending.pop()
            if isinstance(item, dict):
                for key in ("$ref", "$dynamicRef"):
                    if key in item and (not isinstance(item[key], str) or not item[key].startswith("#")):
                        self.error("external_schema_reference", "references/lab.schema.json", "Schema references must stay within this document; remote resolution is disabled.")
                pending.extend(item.values())
            elif isinstance(item, list):
                pending.extend(item)
        if not self.report["ok"]:
            return None
        try:
            schema_validator.check_schema(schema)
            errors = sorted(schema_validator(schema).iter_errors(manifest), key=lambda error: str(list(error.absolute_path)))
            for error in errors:
                location = "/".join(str(part) for part in error.absolute_path)
                self.error("manifest_schema", f"references/lab.json/{location}", error.message)
        except Exception as exc:  # Includes malformed or unresolved local JSON Schema references.
            self.error("invalid_schema", "references/lab.schema.json", str(exc))
        self.report["checked"].append("manifest_schema")
        return manifest if self.report["ok"] else None

    def check_paths(self, manifest: dict) -> None:
        paths = list(manifest["entrypoints"].values()) + manifest["shared_sources"]
        paths += [manifest["environment"][key] for key in ("requirements", "config_example", "config_loader")]
        paths += [resource["defined_in"] for resource in manifest["resources"]]
        for task in manifest["tasks"]:
            paths += [task[key] for key in ("docs", "reference", "troubleshooting")]
            paths += task["concepts"] + task["source_files"]
        for value in paths:
            resolved = self.path(value, "references/lab.json")
            if Path(value).name == "common" and Path(value).parent.name == "src":
                expected = self.root / "support/common"
                if not (self.root / value).is_symlink() or resolved != expected.resolve() or not all((expected / name).is_file() for name in ("__init__.py", "config.py")):
                    self.error("shared_package_link", value, "Preserve this symlink to support/common with its __init__.py and config.py; a text file or another package is not equivalent.")
        # Archive tools can silently expand the shared links into copies. Those
        # copies change __file__-relative paths and drift from the shared code.
        expected = (self.root / "support/common").resolve()
        for source in sorted((self.root / "scripts").glob("*/src")):
            common = source / "common"
            if not common.is_symlink() or common.resolve() != expected:
                self.error(
                    "shared_package_link", str(common.relative_to(self.root)),
                    "Expected a relative symlink to ../../../support/common; preserve links when packaging and extracting the lab.",
                )
        self.report["checked"].append("declared_paths")

    def check_graph(self, manifest: dict) -> None:
        tasks = manifest["tasks"]
        ids = [task["id"] for task in tasks]
        if len(set(ids)) != len(ids):
            self.error("duplicate_task", "references/lab.json", "Task IDs must be unique.")
        if [task["number"] for task in tasks] != list(range(1, len(tasks) + 1)):
            self.error("task_order", "references/lab.json", "Tasks must be listed in consecutive teaching order from 1.")
        graph = {task["id"]: task["requires"] for task in tasks}
        order = {task["id"]: task["number"] for task in tasks}
        for task_id, dependencies in graph.items():
            for dependency in dependencies:
                if dependency not in graph:
                    self.error("unknown_prerequisite", task_id, f"Unknown prerequisite task: {dependency}")
                elif order[dependency] >= order[task_id]:
                    self.error("prerequisite_order", task_id, "A prerequisite must precede its dependent task in the teaching order.")
        active, visited = set(), set()

        def visit(task_id):
            if task_id in active:
                self.error("cyclic_prerequisite", task_id, "Task prerequisites contain a cycle.")
                return
            if task_id in visited or task_id not in graph:
                return
            active.add(task_id)
            for dependency in graph[task_id]:
                visit(dependency)
            active.remove(task_id)
            visited.add(task_id)

        for task_id in graph:
            visit(task_id)
        for resource in manifest["resources"]:
            for task_id in resource["used_by"]:
                if task_id not in graph:
                    self.error("unknown_resource_task", resource["id"], f"Unknown task in used_by: {task_id}")
        self.report["counts"]["tasks"] = len(tasks)
        self.report["checked"].append("task_graph")

    def check_commands(self, manifest: dict) -> None:
        for task in manifest["tasks"]:
            command_ids = set()
            for command in task["commands"]:
                label = f"{task['id']}/{command['id']}"
                if command["id"] in command_ids:
                    self.error("duplicate_command", label, "Command IDs must be unique within a task.")
                command_ids.add(command["id"])
                cwd = self.path(command["cwd"], label)
                if cwd is None:
                    continue
                if not cwd.is_dir():
                    self.error("command_cwd", label, "Command cwd must be a directory.")
                    continue
                argv = command["argv"]
                if re.fullmatch(r"python(?:3(?:\.\d+)?)?", Path(argv[0]).name):
                    source, arguments = None, []
                    if "-m" in argv:
                        i = argv.index("-m") + 1
                        module = argv[i] if i < len(argv) else ""
                        if module.startswith("src."):
                            source = cwd.joinpath(*module.split("."))
                            source = source / "__main__.py" if source.is_dir() else source.with_suffix(".py")
                            source = self.path(str(source.relative_to(self.root)), label)
                            arguments = argv[i + 1:]
                    elif len(argv) > 1 and argv[1].endswith(".py"):
                        source = self.path(argv[1], label, base=cwd)
                        arguments = argv[2:]
                    if source and source.is_file():
                        self.check_flags(source, arguments, label)
                elif Path(argv[0]).name == "make":
                    makefile = self.path("Makefile", label, base=cwd)
                    if makefile:
                        targets = set()
                        for match in re.finditer(r"^([\w. -]+)\s*:(?!=)", makefile.read_text(encoding="utf-8"), re.M):
                            targets.update(match[1].split())
                        for target in argv[1:]:
                            if not target.startswith("-") and "=" not in target and target not in targets:
                                self.error("missing_make_target", label, f"Makefile has no target: {target}")
        self.report["checked"].append("command_sources")

    def check_flags(self, source: Path, arguments: list[str], label: str) -> None:
        """Check literal argparse option names, not argument values or execution."""
        try:
            with tokenize.open(source) as handle:
                tree = ast.parse(handle.read())
        except (SyntaxError, UnicodeError):
            return  # The Python syntax check reports this separately.
        calls = [node for node in ast.walk(tree) if isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute) and node.func.attr == "add_argument"]
        if not calls:
            return
        options = {"--help", "-h"}
        options.update(argument.value for call in calls for argument in call.args if isinstance(argument, ast.Constant) and isinstance(argument.value, str) and argument.value.startswith("-"))
        for argument in arguments:
            if argument == "--":
                break
            option = argument.split("=", 1)[0]
            if re.match(r"--?[A-Za-z]", option) and option not in options:
                self.error("unknown_command_flag", label, f"No literal argparse option {option} in {source.relative_to(self.root)}.")

    def check_links(self, documents: dict[Path, str]) -> None:
        for document, text in documents.items():
            context = str(document.relative_to(self.root))
            plain, _ = markdown_parts(text)
            plain = re.sub(r"(`+).*?\1", "", plain)
            normalize = lambda value: " ".join(value.lower().split())
            labels = {normalize(label) for label in re.findall(r"^ {0,3}\[([^]\n]+)\]:", plain, re.M)}
            for label, reference in re.findall(r"\[([^]\n]+)\]\[([^]\n]*)\]", plain):
                if normalize(reference or label) not in labels:
                    self.error("missing_reference", context, f"Undefined Markdown link reference: {reference or label}")
            for link in markdown_links(text):
                url = urlsplit(link)
                if url.scheme or url.netloc:
                    continue
                target = self.path(unquote(url.path) or document.name, context, base=document.parent)
                if target and url.fragment and target.suffix.lower() == ".md":
                    target_text = documents.get(target)
                    if target_text is None:
                        self.error("unchecked_link_target", context, f"Markdown target is outside checked document directories: {link}")
                    elif unquote(url.fragment) not in anchors(target_text):
                        self.error("missing_anchor", context, f"No such heading or HTML anchor: {link}")
        self.report["counts"]["markdown_files"] = len(documents)
        self.report["checked"].append("markdown_links")

    def check_skills(self, documents: dict[Path, str], yaml) -> None:
        skills = [path for path in documents if path.name == "SKILL.md"]
        if not skills:
            self.error("missing_skill", ".agents/skills", "Expected a repository-local SKILL.md.")
        for path in skills:
            context = str(path.relative_to(self.root))
            match = re.match(r"\A---\s*\n(.*?)\n---\s*(?:\n|$)", documents[path], re.S)
            try:
                frontmatter = yaml.safe_load(match[1]) if match else None
                if not isinstance(frontmatter, dict):
                    raise ValueError("Expected YAML frontmatter between --- lines.")
                name, description = frontmatter.get("name"), frontmatter.get("description")
                if not isinstance(name, str) or len(name) > 64 or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", name) or name != path.parent.name:
                    raise ValueError("Skill name must be lowercase hyphenated text matching its folder (maximum 64 characters).")
                if not isinstance(description, str) or not description.strip() or len(description) > 1024:
                    raise ValueError("Skill description must be nonempty text (maximum 1024 characters).")
            except (ValueError, yaml.YAMLError) as exc:
                self.error("skill_frontmatter", context, str(exc))
        self.report["checked"].append("skill_frontmatter")

    def check_tasks(self, manifest: dict, documents: dict[Path, str]) -> None:
        declared = {task["docs"] for task in manifest["tasks"]}
        for path in documents:
            if path.parent == self.root / "tasks" and str(path.relative_to(self.root)) not in declared:
                self.error("unregistered_task", str(path.relative_to(self.root)), "Register this task in references/lab.json.")
        for task in manifest["tasks"]:
            path = self.root / task["docs"]
            text = documents.get(path, "")
            plain, blocks = markdown_parts(text)
            headings = re.findall(r"^## (.+)$", plain, re.M)
            steps = [heading for heading in headings if re.match(r"Step \d+: ", heading)]
            numbers = [int(re.match(r"Step (\d+):", heading)[1]) for heading in steps]
            expected = ["What you will learn"]
            understand = [heading for heading in headings if heading.startswith("Understand ")]
            expected += understand + ["Before you begin"] + steps + ["Completion checkpoint"]
            title = f"# Task {task['number']}: {task['title']}"
            if not plain.startswith(title + "\n") or len(understand) != 1 or headings != expected:
                self.error("task_headings", task["docs"], "Keep the Task title, What you will learn, Understand topic, Before you begin, Steps, and Completion checkpoint in order.")
            if not steps or steps[0] != "Step 1: enter the existing workspace" or numbers != list(range(1, len(steps) + 1)):
                self.error("task_steps", task["docs"], "Use consecutive steps beginning with Step 1: enter the existing workspace.")
            learn = plain.partition("## What you will learn\n")[2].split("\n## ", 1)[0]
            rows = re.findall(r"^\|\s*(\[[0-9]+\]\([^)]+\)|[0-9]+)\s*\|([^\n]+)$", learn, re.M)
            table_numbers = [int(re.search(r"[0-9]+", row[0])[0]) for row in rows]
            if "The practical sequence is:" not in learn or not re.search(r"\|\s*Step\s*\|\s*Activity\s*\|\s*Checkpoint\s*\|", learn) or table_numbers != numbers:
                self.error("task_sequence", task["docs"], "The practical-sequence table needs one numbered row per step in the same order.")
            for cell, rest in rows:
                link = re.search(r"\]\(([^)]+)\)", cell)
                number = int(re.search(r"[0-9]+", cell)[0])
                if link and number <= len(steps) and link[1] != "#" + slug(steps[number - 1]):
                    self.error("task_sequence", task["docs"], f"Step {number}'s table link must point to its own heading.")
                if len(rest.strip().rstrip("|").split("|")) != 2 or any(not cell.strip() for cell in rest.strip().rstrip("|").split("|")):
                    self.error("task_sequence", task["docs"], "Every sequence row needs an activity and a checkpoint.")
            if not any(re.match(r"\.\./(?:scripts|support)/", link) for link in markdown_links(text)):
                self.error("task_source", task["docs"], "Include an inline link to the checked-in source or configuration used by this task.")
            lines = text.splitlines()
            for start, end, language in blocks:
                if language == "unclosed":
                    self.error("markdown_fence", task["docs"], f"Unclosed code fence on line {start + 1}.")
                if language not in EXECUTABLE_LANGUAGES:
                    continue
                before = "\n".join(lines[:start]).rstrip().split("\n\n")[-1]
                if "Implementation excerpt (not a standalone exercise)" in before:
                    continue
                after = []
                for line in lines[end + 1:]:
                    if re.match(r"^\s{0,3}(?:#{1,6} |`{3,}|~{3,})", line):
                        break
                    after.append(line)
                if not re.search(r"(?m)^\s*(?:\*\*)?Expected(?:\*\*)?:", "\n".join(after)):
                    self.error("task_expected", task["docs"], f"Executable block on line {start + 1} needs Expected: before the next section or exercise.")
        for path, text in documents.items():
            if path == self.root / "README.md" or path.parent == self.root / "tasks":
                prose, _ = markdown_parts(text)
                prose = re.sub(r"(`+).*?\1", "", prose)
                match = AUTHOR_TOKENS.search(text) or PROSE_AUTHOR_TOKENS.search(prose)
                if match:
                    self.error("author_token", str(path.relative_to(self.root)), f"Resolve the learner-facing author token: {match[0]}")
        self.report["checked"].append("task_structure")

    def check_python(self, files: list[Path]) -> None:
        sources = [path for path in files if path.suffix == ".py"]
        for path in sources:
            try:
                with tokenize.open(path) as handle:
                    ast.parse(handle.read(), filename=str(path))
            except (SyntaxError, OSError, UnicodeError) as exc:
                self.error("python_syntax", str(path.relative_to(self.root)), str(exc))
        self.report["counts"]["python_files"] = len(sources)
        self.report["checked"].append("python_syntax")


def validate(root: Path) -> dict:
    checker = LabValidator(root)
    try:
        from jsonschema import Draft202012Validator
        import yaml
    except ImportError as exc:
        checker.error("missing_dependency", "support/requirements-test.txt", f"Install the validation dependencies: python -m pip install -r support/requirements-test.txt ({exc.name}).")
        return checker.report
    manifest = checker.check_schema(Draft202012Validator)
    if manifest is None:
        return checker.report
    try:
        files = checker.files()
        documents = {path: path.read_text(encoding="utf-8") for path in files if path.suffix.lower() == ".md"}
        checker.check_paths(manifest)
        checker.check_graph(manifest)
        checker.check_commands(manifest)
        checker.check_links(documents)
        checker.check_skills(documents, yaml)
        checker.check_tasks(manifest, documents)
        checker.check_python(files)
    except (OSError, UnicodeError, ValueError, KeyError, TypeError, IndexError) as exc:
        checker.error("invalid_input", ".", str(exc))
    return checker.report


def main() -> int:
    root = Path(__file__).resolve().parents[1]
    if len(sys.argv) != 1:
        checker = LabValidator(root)
        checker.error("unsupported_argument", "scripts/validate_lab.py", "This offline checker takes no command-line arguments.")
        report = checker.report
    else:
        report = validate(root)
    print(json.dumps(report, ensure_ascii=False))
    if any(error["code"] == "missing_dependency" for error in report["errors"]):
        return 2
    return 0 if report["ok"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
