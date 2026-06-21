import re

with open("go/internal/erp/ase/dags/ase.yml", "r") as f:
    content = f.read()

def replacer(match):
    json_str = match.group(1)
    
    lines = json_str.split('\n')
    
    out = '    {\n      "rows": {\n        "row_id_1": {\n'
    
    # We skip the first line { and last line }
    # Let's find the first {
    start_idx = 0
    for i, l in enumerate(lines):
        if '{' in l:
            start_idx = i
            break
            
    end_idx = len(lines) - 1
    for i in range(len(lines)-1, -1, -1):
        if '}' in lines[i]:
            end_idx = i
            break
            
    for line in lines[start_idx+1:end_idx]:
        out += '    ' + line + '\n'
        
    out += '        }\n      }\n    }'
    
    return "EXPECTED OUTPUT FORMAT:\n" + out

# Using a more robust regex that stops at the closing brace of the JSON
new_content = re.sub(r'EXPECTED OUTPUT FORMAT:\s*(\{\s*"property".*?\n\s*\})', replacer, content, flags=re.DOTALL)

with open("go/internal/erp/ase/dags/ase.yml", "w") as f:
    f.write(new_content)

print(f"Replaced {len(re.findall(r'EXPECTED OUTPUT FORMAT:\s*(\{\s*\"property\".*?\n\s*\})', content, flags=re.DOTALL))} occurrences.")
