import re
import json
from pathlib import Path


def parse_plan_comptable(file_path):
    account_dict = {}
    with open(file_path, encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            code, label = line.split(" ", 1)
            account_dict[code] = label.strip()
    return account_dict

if __name__ == "__main__":
    txt_path = Path(__file__).parent / "plan_comptable_marocain.txt"
    out_path = Path(__file__).parent / "plan_comptable_marocain_dict1.py"

    account_dict = parse_plan_comptable(txt_path)

    # Use json.dumps to ensure double quotes
    dict_str = json.dumps(account_dict, ensure_ascii=False, indent=2)
    with open(out_path, "w", encoding="utf-8") as f:
        f.write("# Auto-generated from plan_comptable_marocain.txt\n")
        f.write(f"PLAN_COMPTABLE_MAROCAIN = {dict_str}\n")

    print(f"Extracted {len(account_dict)} account codes to {out_path}")