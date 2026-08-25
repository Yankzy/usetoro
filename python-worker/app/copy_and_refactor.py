import os
import shutil
from pathlib import Path

base_dir = Path("/Users/Yankz/programming/usetoro/python-worker/app")
eval_dir = base_dir / "reconciliation_eval"
prod_dir = base_dir / "reconciliation_prod"

def setup_dirs():
    prod_dir.mkdir(exist_ok=True)
    (prod_dir / "domain").mkdir(exist_ok=True)
    (prod_dir / "routing").mkdir(exist_ok=True)
    (prod_dir / "reconciliation").mkdir(exist_ok=True)
    
    (prod_dir / "__init__.py").touch()
    (prod_dir / "domain" / "__init__.py").touch()
    (prod_dir / "routing" / "__init__.py").touch()
    (prod_dir / "reconciliation" / "__init__.py").touch()

def refactor_file(src_path: Path, dst_path: Path):
    if not src_path.exists():
        print(f"Error: {src_path} does not exist.")
        return
    content = src_path.read_text()
    # Replace imports
    # from domain... to from reconciliation_prod.domain...
    # from routing... to from reconciliation_prod.routing...
    # from agent... to from reconciliation_prod.reconciliation...
    # from optimizer... to from reconciliation_prod.reconciliation...
    # from validation... to from reconciliation_prod.reconciliation...
    
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
            
        # The PRD said book.py instead of books.py, and state.py instead of patch.py
        line = line.replace("domain.books", "domain.book")
        line = line.replace("domain.patch", "domain.state")
        line = line.replace("reconciliation_prod.domain.books", "reconciliation_prod.domain.book")
        line = line.replace("reconciliation_prod.domain.patch", "reconciliation_prod.domain.state")
        
        new_lines.append(line)
        
    dst_path.write_text('\n'.join(new_lines))

def main():
    setup_dirs()
    
    # Domain
    refactor_file(eval_dir / "domain" / "bank.py", prod_dir / "domain" / "bank.py")
    refactor_file(eval_dir / "domain" / "books.py", prod_dir / "domain" / "book.py")
    refactor_file(eval_dir / "domain" / "hypothesis.py", prod_dir / "domain" / "hypothesis.py")
    refactor_file(eval_dir / "domain" / "patch.py", prod_dir / "domain" / "state.py")
    refactor_file(eval_dir / "domain" / "base.py", prod_dir / "domain" / "base.py") # Might be needed
    
    # Routing
    refactor_file(eval_dir / "routing" / "feasibility.py", prod_dir / "routing" / "feasibility.py")
    refactor_file(eval_dir / "routing" / "scorer.py", prod_dir / "routing" / "scorer.py")
    refactor_file(eval_dir / "routing" / "optimizer.py", prod_dir / "routing" / "optimizer.py")
    refactor_file(eval_dir / "routing" / "orchestrator.py", prod_dir / "routing" / "engine.py")
    
    # Reconciliation
    refactor_file(eval_dir / "agent" / "candidate_generation.py", prod_dir / "reconciliation" / "candidate_generation.py")
    refactor_file(eval_dir / "optimizer" / "cp_sat.py", prod_dir / "reconciliation" / "cp_sat.py")
    refactor_file(eval_dir / "optimizer" / "model.py", prod_dir / "reconciliation" / "model.py")
    refactor_file(eval_dir / "validation" / "deterministic.py", prod_dir / "reconciliation" / "validation.py")

if __name__ == "__main__":
    main()
