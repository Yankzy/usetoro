import json
from pathlib import Path

COA_PATH = Path(__file__).parent / "chart_of_accounts_with_types.json"

def load_coa_json():
    with open(COA_PATH, "r", encoding="utf-8") as f:
        return json.load(f)

def extract_concrete_accounts(accounts, parent=None):
    """
    Recursively extract all concrete accounts, applying duplicate code logic.
    """
    result = []
    if isinstance(accounts, dict):
        accounts = [accounts]
    for acc in accounts:
        if acc.get("type") == "concrete":
            result.append(acc)
        elif acc.get("children"):
            # Handle duplicate name/code logic among siblings
            children = acc["children"]
            # Group by name
            name_map = {}
            for child in children:
                name_map.setdefault(child["name"], []).append(child)
            for name, group in name_map.items():
                if len(group) == 2:
                    codes = [c["code"] for c in group]
                    # If one code is the other with a trailing '0', only use the one without the '0'
                    if (codes[0] == codes[1] + "0") or (codes[1] == codes[0] + "0"):
                        # Use the one without the trailing '0'
                        chosen = min(group, key=lambda c: len(c["code"]))
                        if chosen.get("type") == "concrete":
                            result.append(chosen)
                        elif chosen.get("children"):
                            result.extend(extract_concrete_accounts(chosen["children"], chosen))
                        continue  # skip the other
                # Otherwise, use all
                for child in group:
                    if child.get("type") == "concrete":
                        result.append(child)
                    elif child.get("children"):
                        result.extend(extract_concrete_accounts(child["children"], child))
        # If no children and not concrete, skip
    return result

def get_available_account_codes():
    coa = load_coa_json()
    return extract_concrete_accounts(coa)