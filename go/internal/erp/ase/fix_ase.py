import re

with open('go/internal/erp/ase/ase.yml', 'r') as f:
    content = f.read()

def replace_expected_output(match):
    original_json = match.group(1)
    
    # We want to extract the content inside "rows": { ... }
    # original_json is the block inside:
    # {
    #   "rows": {
    #     "row_id_1": {
    #        ...
    #     }
    #   }
    # }
    
    # Let's extract the "row_id_1" block
    row_block_match = re.search(r'"rows":\s*\{\s*("row_id_1":.*?)\s*\}\s*\}', original_json, flags=re.DOTALL)
    if not row_block_match:
        # Fallback if the regex fails
        return match.group(0)
    
    row_block = row_block_match.group(1)
    
    # Now format it into an RFC 6902 patch
    # We'll indent the row_block properly
    lines = row_block.split('\n')
    indented_row_block = '\n'.join('          ' + line.strip() for line in lines)
    
    new_format = f"""EXPECTED OUTPUT FORMAT:
    [
      {{
        "op": "add",
        "path": "/rows",
        "value": {{
{indented_row_block}
        }}
      }}
    ]"""
    return new_format

new_content = re.sub(r'EXPECTED OUTPUT FORMAT:\s*(\{.*?\n    \})', replace_expected_output, content, flags=re.DOTALL)

with open('go/internal/erp/ase/ase.yml', 'w') as f:
    f.write(new_content)
print("Done formatting EXPECTED OUTPUT FORMAT!")
