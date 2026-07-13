import yaml
from collections import defaultdict, deque
from typing import Dict, List, Tuple, Set

def validate_dag(yaml_string: str) -> Tuple[bool, List[str]]:
    """
    Validates a DAG configuration for:
    1. Valid YAML compilation.
    2. Prompt existence alignment (prompt_key map checking).
    3. Missing child node structural definitions.
    4. Cyclical loops (via Topological Sort / Kahn's Algorithm).
    5. Dead-end/Unreachable node discovery from the entry point.
    """
    errors = []
    
    # --- STEP 1: Parse YAML Payload ---
    try:
        config = yaml.safe_load(yaml_string)
    except yaml.YAMLError as e:
        return False, [f"Critical YAML Syntax Error: {e}"]
        
    if not config:
        return False, ["Validation Failed: Document is empty."]

    # Extract target configuration sections
    prompts_dict = config.get("prompts", {})
    defined_prompts: Set[str] = set(prompts_dict.keys())
    
    dag_section = config.get("dag", {})
    entry_node = dag_section.get("entry_node")
    nodes_dict = dag_section.get("nodes", {})

    if not nodes_dict:
        return False, ["Validation Failed: No nodes defined under the 'dag.nodes' configuration schema."]

    if entry_node not in nodes_dict:
        errors.append(f"Root Configuration Error: entry_node '{entry_node}' is missing from the nodes repository.")

    # Track graph structures for Kahn's Algorithm
    adjacency_list = defaultdict(list)
    in_degree = {node_id: 0 for node_id in nodes_dict}
    resume_edges: Dict[str, str] = {}  # runtime re-entry edges (excluded from cycle detection)

    # --- STEP 2: Prompt Key & Structural Pointer Validation ---
    for node_id, node_config in nodes_dict.items():
        if not node_config:
            errors.append(f"Node Blueprint Error: Node '{node_id}' contains an empty configurations mapping block.")
            continue

        # Check prompt_key validation alignment
        prompt_key = node_config.get("prompt_key")
        if prompt_key and prompt_key not in defined_prompts:
            errors.append(f"Semantic Alignment Error: Node '{node_id}' requires undefined prompt target: '{prompt_key}'")

        # Gather target children across edge paradigms
        expected_children: List[str] = []
        
        # 1. Parse static/dynamic routing children map dictionaries
        node_children = node_config.get("children")
        if isinstance(node_children, dict):
            expected_children.extend(node_children.values())
        elif isinstance(node_children, list):
            expected_children.extend(node_children)

        # 2. Validate resume_child targets exist but do NOT add them to the
        #    structural adjacency graph. resume_child edges are runtime
        #    re-entry points (back-edges by design) used when a hold node is
        #    resolved — they intentionally point upstream and would create
        #    false-positive cycles in topological sort.
        resume_child = node_config.get("resume_child")
        if resume_child:
            if resume_child not in nodes_dict:
                errors.append(f"Topology Breakdown: Node '{node_id}' references non-existent resume_child destination: '{resume_child}'")
            else:
                # Track for reachability analysis only (not cycle detection)
                resume_edges[node_id] = resume_child

        # Map structural dependencies and validate integrity
        for child_target in expected_children:
            if child_target not in nodes_dict:
                errors.append(f"Topology Breakdown: Node '{node_id}' references non-existent destination node: '{child_target}'")
            else:
                adjacency_list[node_id].append(child_target)
                in_degree[child_target] += 1

    # If parsing errors already exist, abort before running topological analysis
    if errors:
        return False, errors

    # --- STEP 3: Topological Sort & Cycle Detection (Kahn's Algorithm) ---
    # Enqueue nodes with zero dependencies
    zero_in_degree_queue = deque([node for node, degree in in_degree.items() if degree == 0])
    processed_node_count = 0
    topological_execution_order = []

    while zero_in_degree_queue:
        current_node = zero_in_degree_queue.popleft()
        processed_node_count += 1
        topological_execution_order.append(current_node)

        for child in adjacency_list[current_node]:
            in_degree[child] -= 1
            if in_degree[child] == 0:
                zero_in_degree_queue.append(child)

    # If processed nodes do not equal total nodes, a loop exists
    if processed_node_count != len(nodes_dict):
        cyclical_nodes = [node for node, degree in in_degree.items() if degree > 0]
        errors.append(f"Cyclical Feedback Loop Detected! The following execution nodes form a closed loop blocking processing: {cyclical_nodes}")

    # --- STEP 4: Reachability Analysis ---
    # Ensure no orphan pipelines exist outside the tracking interface
    reachable_nodes = set()
    
    def traverse_graph(node: str):
        if node in reachable_nodes:
            return
        reachable_nodes.add(node)
        for child in adjacency_list[node]:
            traverse_graph(child)
        # Also follow resume_child edges for reachability (they are valid
        # runtime paths, just not structural DAG edges for cycle detection)
        if node in resume_edges:
            traverse_graph(resume_edges[node])

    if entry_node in nodes_dict:
        traverse_graph(entry_node)
        unreachable_nodes = set(nodes_dict.keys()) - reachable_nodes
        if unreachable_nodes:
            errors.append(f"Orphan Pipeline Warning: The following nodes are completely disconnected from entry point '{entry_node}': {list(unreachable_nodes)}")

    # --- STEP 5: Final Evaluation Output ---
    if errors:
        return False, errors
    return True, topological_execution_order

import sys
import os

# --- RUNTIME EXECUTION EXAMPLES ---
# Execute with: python validate_dag.py
# Or with a specific file: python validate_dag.py marketing_dag.yaml

if __name__ == "__main__":
    print("Executing DAG Validation Diagnostics...")
    print("-" * 50)
    
    # 1. Determine target file path from CLI args or default fallback
    if len(sys.argv) > 1:
        file_path = sys.argv[1]
    else:
        file_path = "marketing_dag.yaml"  # Default configuration file name
        
    print(f"Target Source Configuration Path: '{file_path}'\n")
    
    # 2. Structural Guardrail: Validate file existence before processing
    if not os.path.exists(file_path):
        print(f"❌ FAILURE: Configuration target not found at file path: '{file_path}'")
        print("\nTo target a specific file, pass it as an argument:")
        print(f"  python {os.path.basename(sys.argv[0] if sys.argv else 'validate_dag.py')} {file_path}")
        sys.exit(1)
        
    # 3. Securely ingest file stream payload
    try:
        with open(file_path, "r", encoding="utf-8") as file:
            yaml_payload = file.read()
    except Exception as e:
        print(f"❌ FAILURE: Could not open or read target payload file stream: {e}")
        sys.exit(1)
        
    # 4. Invoke Validation Routine (using the function defined in the previous step)
    is_valid, report = validate_dag(yaml_payload)
    
    # 5. Output Evaluation Log
    if is_valid:
        print("✅ SUCCESS: DAG Configuration is valid and non-cyclic!")
        print(f"Optimal Topological Execution Sequence:\n  {report}")
    else:
        print("❌ FAILURE: Pipeline configuration errors identified:\n")
        for error in report:
            print(f"  • {error}")