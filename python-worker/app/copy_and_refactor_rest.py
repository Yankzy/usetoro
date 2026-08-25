import os
from pathlib import Path

base_dir = Path("/Users/Yankz/programming/usetoro/python-worker/app")
eval_dir = base_dir / "reconciliation_eval"
prod_dir = base_dir / "reconciliation_prod"

def refactor_file(src_path: Path, dst_path: Path):
    if not src_path.exists():
        print(f"Error: {src_path} does not exist.")
        return
    content = src_path.read_text()
    
    lines = content.split('\n')
    new_lines = []
    for line in lines:
        if line.startswith("from domain"):
            line = line.replace("from domain", "from reconciliation_prod.domain")
        elif line.startswith("import domain"):
            line = line.replace("import domain", "import reconciliation_prod.domain")
            
        if line.startswith("from routing"):
            line = line.replace("from routing", "from reconciliation_prod.routing")
        elif line.startswith("import routing"):
            line = line.replace("import routing", "import reconciliation_prod.routing")
            
        if line.startswith("from agent"):
            line = line.replace("from agent", "from reconciliation_prod.reconciliation")
        elif line.startswith("import agent"):
            line = line.replace("import agent", "import reconciliation_prod.reconciliation")
            
        if line.startswith("from optimizer"):
            line = line.replace("from optimizer", "from reconciliation_prod.reconciliation")
        elif line.startswith("import optimizer"):
            line = line.replace("import optimizer", "import reconciliation_prod.reconciliation")
            
        if line.startswith("from validation"):
            line = line.replace("from validation", "from reconciliation_prod.reconciliation")
        elif line.startswith("import validation"):
            line = line.replace("import validation", "import reconciliation_prod.reconciliation")
            
        line = line.replace("domain.books", "domain.book")
        line = line.replace("domain.patch", "domain.state")
        line = line.replace("reconciliation_prod.domain.books", "reconciliation_prod.domain.book")
        line = line.replace("reconciliation_prod.domain.patch", "reconciliation_prod.domain.state")
        
        new_lines.append(line)
        
    dst_path.write_text('\n'.join(new_lines))

def main():
    files = ["protocol.py", "alternatives.py", "diagnostics.py", "validation.py"]
    for f in files:
        if f == "validation.py":
            # the source might be reconciliation_eval/optimizer/validation.py 
            # wait, my previous script mapped reconciliation_eval/validation/deterministic.py to reconciliation_prod/reconciliation/validation.py
            # let's map optimizer/validation.py to optimizer_validation.py to avoid collision, or just append it.
            # actually, let's look at what optimizer/validation.py is vs validation/deterministic.py
            pass
        refactor_file(eval_dir / "optimizer" / f, prod_dir / "reconciliation" / f)
        
if __name__ == "__main__":
    main()
