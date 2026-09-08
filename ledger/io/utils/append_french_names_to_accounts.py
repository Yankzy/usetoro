# File: ledger-be_py/ledger/io/utils/append_french_names_to_accounts.py

import json
from pathlib import Path
from plan_comptable_marocain_dict1 import PLAN_COMPTABLE_MAROCAIN

JSON_PATH = Path(__file__).parent / "chart_of_accounts_with_roles.json"
OUTPUT_PATH = Path(__file__).parent / "chart_of_accounts_with_roles_with_fr.json"

def append_french_names(obj):
    """
    Recursively traverse obj (dict or list), and for every dict with a 'code' key,
    append the French name to the 'name' field if available.
    """
    if isinstance(obj, dict):
        code = obj.get("code")
        if code is not None:
            fr_name = PLAN_COMPTABLE_MAROCAIN.get(str(code))
            if fr_name:
                en_name = obj.get("name", "")
                # Avoid double-appending
                if fr_name not in en_name:
                    obj["name"] = f"{en_name} / {fr_name}"
        # Recurse into all values
        for v in obj.values():
            append_french_names(v)
    elif isinstance(obj, list):
        for item in obj:
            append_french_names(item)

def main():
    with open(JSON_PATH, "r", encoding="utf-8") as f:
        data = json.load(f)
    append_french_names(data)
    with open(OUTPUT_PATH, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
    print(f"Updated accounts written to {OUTPUT_PATH}")

if __name__ == "__main__":
    main()