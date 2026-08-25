import json
import subprocess
from pathlib import Path
from datetime import date
from . task import prepare_research_task

def run_codex(run: dict) -> dict:
    command = [
        "codex",
        "--search",
        "exec",
        "--model",
        "gpt-5.6-sol",
        "--json",
        "--output-schema",
        run["schema_file"],
        "-o",
        run["brief_file"],
        "-",
    ]

    Path(run["brief_file"]).parent.mkdir(
        parents=True,
        exist_ok=True,
    )

    with open(run["trace_file"], "w", encoding="utf-8") as trace:
        subprocess.run(
            command,
            input=run["prompt"],
            text=True,
            stdout=trace,
            check=True,
        )

    return json.loads(
        Path(run["brief_file"]).read_text(encoding="utf-8")
    )

run = prepare_research_task(
    topic="AI data-center infrastructure",
    as_of=date(2026, 7, 12),
    lookback_days=30,
    max_events=6,
)

brief = run_codex(run)
