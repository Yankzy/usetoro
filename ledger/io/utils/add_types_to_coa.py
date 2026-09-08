import json
from pathlib import Path
from typing import Any, Dict, List, Optional

class TrieNode:
    def __init__(self):
        self.children: Dict[str, 'TrieNode'] = {}
        self.is_code: bool = False
        self.code: Optional[str] = None

def build_trie(codes: List[str]) -> TrieNode:
    root = TrieNode()
    for code in codes:
        node = root
        for char in code:
            if char not in node.children:
                node.children[char] = TrieNode()
            node = node.children[char]
        node.is_code = True
        node.code = code
    return root

def find_longest_prefix(code: str, trie: TrieNode) -> Optional[str]:
    node = trie
    last_code = None
    for i, char in enumerate(code):
        if char in node.children:
            node = node.children[char]
            if node.is_code and i < len(code) - 1:
                last_code = node.code
        else:
            break
    return last_code

def nest_accounts_trie(flat_accounts: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    code_map = {acc["code"]: dict(acc) for acc in flat_accounts}
    codes = list(code_map.keys())
    trie = build_trie(codes)
    for acc in code_map.values():
        acc["children"] = []
    roots = []
    for code, acc in code_map.items():
        parent_code = find_longest_prefix(code, trie)
        if parent_code:
            code_map[parent_code]["children"].append(acc)
        else:
            roots.append(acc)
    for acc in code_map.values():
        if not acc["children"]:
            del acc["children"]
    return roots

def annotate_types(node: Dict[str, Any]) -> Dict[str, Any]:
    children = node.get("children")
    if children and len(children) > 0:
        node["type"] = "abstract"
        node["children"] = [annotate_types(child) for child in children]
    else:
        node["type"] = "concrete"
    return node

def process_class_node(class_node: Dict[str, Any]) -> Dict[str, Any]:
    children = class_node.get("children", [])
    if not children:
        class_node["type"] = "concrete"
        return class_node
    # Flatten all descendants
    def collect_accounts(node):
        accs = [dict(node)]
        for child in node.get("children", []):
            accs.extend(collect_accounts(child))
        return accs
    flat_accounts = []
    for child in children:
        flat_accounts.extend(collect_accounts(child))
    for acc in flat_accounts:
        acc.pop("children", None)
    nested = nest_accounts_trie(flat_accounts)
    class_node["children"] = [annotate_types(node) for node in nested]
    class_node["type"] = "abstract"
    return class_node

def main(input_path: str, output_path: str) -> None:
    with open(input_path, "r", encoding="utf-8") as f:
        coa = json.load(f)
    processed = [process_class_node(class_node) for class_node in coa]
    with open(output_path, "w", encoding="utf-8") as f:
        json.dump(processed, f, indent=2, ensure_ascii=False)
    print(f"Trie-nested and annotated CoA written to {output_path}")

if __name__ == "__main__":
    input_file = Path(__file__).parent / "chart_of_accounts_with_roles.json"
    output_file = Path(__file__).parent / "chart_of_accounts_with_types.json"
    main(input_file, output_file)