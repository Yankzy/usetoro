from datetime import date, timedelta

def prepare_research_task(
    topic: str,
    as_of: date,
    lookback_days: int,
    max_events: int,
) -> dict:
    window_end = as_of
    window_start = as_of - timedelta(days=lookback_days - 1)

    prompt = (
        "PROMPT_HERE: Research the topic '{{TOPIC}}' and provide a brief summary of relevant events "
        .replace("{{TOPIC}}", topic)
        .replace("{{WINDOW_START}}", window_start.isoformat())
        .replace("{{WINDOW_END}}", window_end.isoformat())
        .replace("{{MAX_EVENTS}}", str(max_events))
    )

    return {
        "prompt": prompt,
        "schema_file": "schemas/evidence_brief.schema.json",
        "brief_file": "outputs/brief.json",
        "trace_file": "outputs/run.jsonl",
    }