
import json
from pathlib import Path
import json
from pathlib import Path
import sys
import importlib.util

default_coas_path = Path(__file__).parent / "default_coas.py"
spec = importlib.util.spec_from_file_location("default_coas", default_coas_path)
default_coas = importlib.util.module_from_spec(spec)
spec.loader.exec_module(default_coas)
categories = default_coas.categories

# Paths
JSON_PATH = Path(__file__).parent / "chart_of_accounts_with_types.json"

# Paths
JSON_PATH = Path(__file__).parent / "chart_of_accounts_with_types.json"


# 2. Build a code->name mapping from categories
code_to_name = {}
for cat in categories.values():
    for acc in cat["accounts"]:
        code_to_name[acc["code"]] = acc["name"]

# 3. Load JSON
with open(JSON_PATH, "r", encoding="utf-8") as f:
    json_data = json.load(f)

# 4. Recursively update names in JSON if code matches and name differs
def update_names(node):
    if isinstance(node, dict):
        code = node.get("code")
        if code and code in code_to_name:
            if node.get("name") != code_to_name[code]:
                node["name"] = code_to_name[code]
        for child in node.get("children", []):
            update_names(child)
    elif isinstance(node, list):
        for item in node:
            update_names(item)

update_names(json_data)

# 5. Save updated JSON (overwrite or to a new file)
with open(JSON_PATH, "w", encoding="utf-8") as f:
    json.dump(json_data, f, ensure_ascii=False, indent=2)

print("Names updated in JSON where code matched and name differed.")