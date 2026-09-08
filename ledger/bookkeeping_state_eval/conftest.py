from __future__ import annotations

import sys
from pathlib import Path

# Add python-worker/app and python-worker/app/bookkeeping_state_eval to sys.path
_eval_dir = Path(__file__).resolve().parent
_app_dir = _eval_dir.parent

for p in (_app_dir, _eval_dir):
    p_str = str(p)
    if p_str not in sys.path:
        sys.path.insert(0, p_str)
