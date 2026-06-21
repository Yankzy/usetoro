import re

with open('go/internal/erp/ase/dags/ase.yml', 'r') as f:
    text = f.read()

def fix_end(match):
    reasoning = match.group(1)
    return f'reasoning": "{reasoning}" }}\n            }}\n          }}\n        }}\n      }}\n    ]'

new_text = re.sub(r'reasoning": "([^"]*)"\n\s*\}\n\s*\}\n\s*\]', fix_end, text)

with open('go/internal/erp/ase/dags/ase.yml', 'w') as f:
    f.write(new_text)
print("Done fixing braces!")
