import json
import re
from typing import List, Dict, Any
from pathlib import Path

# Set your input and output file paths here
INPUT_TXT = Path(__file__).parent / "plan_comptable_marocain.txt"
OUTPUT_JSON = Path(__file__).parent / "chart_of_accounts_only_fr.json"

def parse_txt_lines(txt_path: str) -> List[Dict[str, str]]:
    accounts = []
    with open(txt_path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            # Example line: 1111 Share capital / Capital social
            match = re.match(r"^([0-9A-Za-z./]+)\s+(.+)$", line)
            if match:
                code, name = match.groups()
                accounts.append({"code": code, "name": name})
    return accounts

def build_trie(accounts: List[Dict[str, str]]) -> List[Dict[str, Any]]:
    code_map = {acc["code"]: {**acc, "children": []} for acc in accounts}
    roots = []
    codes = sorted(code_map.keys(), key=len)
    for code in codes:
        parent = None
        for i in range(len(code) - 1, 0, -1):
            prefix = code[:i]
            if prefix in code_map:
                parent = prefix
                break
        if parent:
            code_map[parent]["children"].append(code_map[code])
        else:
            roots.append(code_map[code])
    for acc in code_map.values():
        if not acc["children"]:
            acc.pop("children")
    return roots

def main():
    accounts = parse_txt_lines(INPUT_TXT)
    tree = build_trie(accounts)
    with open(OUTPUT_JSON, "w", encoding="utf-8") as f:
        json.dump(tree, f, ensure_ascii=False, indent=2)

if __name__ == "__main__":
    main()