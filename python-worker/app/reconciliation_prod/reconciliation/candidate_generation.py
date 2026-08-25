from typing import List
from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.reconciliation.validation import DIRECTION_COMPATIBILITY_MAP
from ortools.sat.python import cp_model

class GroupedSolutionCallback(cp_model.CpSolverSolutionCallback):
    def __init__(self, variables, items, bank_or_book_id, target_amt, prefix, max_solutions=50):
        cp_model.CpSolverSolutionCallback.__init__(self)
        self._variables = variables
        self._items = items
        self._target_id = bank_or_book_id
        self._target_amt = target_amt
        self._prefix = prefix
        self._max_solutions = max_solutions
        self._solution_count = 0
        self.candidates = []

    def OnSolutionCallback(self):
        self._solution_count += 1
        selected = [self._items[i] for i, v in enumerate(self._variables) if self.Value(v)]
        
        # Format the combination
        if self._prefix == "GROUPED":
            combo_str = " + ".join([f"{j.id} ({j.remaining_amount_int})" for j in selected])
            cps = {j.counterparty_id for j in selected if j.counterparty_id}
            cp_str = f" [Counterparty: {','.join(cps)}]" if cps else " [Counterparty: None]"
            self.candidates.append(f"- [{self._prefix}] Bank {self._target_id} ({self._target_amt}) -> Books {combo_str}{cp_str}")
        else:
            combo_str = " + ".join([b.id for b in selected])
            self.candidates.append(f"- [{self._prefix}] Banks {combo_str} ({self._target_amt}) -> Book {self._target_id} ({self._target_amt})")

        if self._solution_count >= self._max_solutions:
            self.StopSearch()

def generate_plausible_candidates(bank_items: List[BankItem], book_items: List[BookItem]) -> str:
    """
    Deterministically generates a list of mathematically plausible candidates 
    (exact, partial, and grouped via CP-SAT) to feed to the LLM for scoring.
    """
    candidates = []
    
    # 1. Linear scan for 1:1 Matches (Exact and Partial)
    for bank in bank_items:
        for book in book_items:
            if (bank.direction, book.direction) not in DIRECTION_COMPATIBILITY_MAP:
                continue
            
            bank_amt = bank.amount_int
            book_amt = book.remaining_amount_int
            
            if bank_amt == book_amt:
                candidates.append(f"- [EXACT] Bank {bank.id} ({bank_amt}) -> Book {book.id} ({book_amt})")
            elif bank_amt < book_amt:
                candidates.append(f"- [PARTIAL] Bank {bank.id} ({bank_amt}) -> Book {book.id} (consumes {bank_amt} of {book_amt})")

    # 2. 1 Bank -> Many Books (Grouped Matches via CP-SAT)
    for bank in bank_items:
        valid_books = [j for j in book_items if (bank.direction, j.direction) in DIRECTION_COMPATIBILITY_MAP]
        if not valid_books:
            continue
            
        model = cp_model.CpModel()
        x = [model.NewBoolVar(f'x_{j.id}') for j in valid_books]
        
        # Must exactly sum to the bank amount
        model.Add(sum(x[i] * valid_books[i].remaining_amount_int for i in range(len(valid_books))) == bank.amount_int)
        
        # Must select at least 2 items (1:1 is handled above)
        model.Add(sum(x) >= 2)
        
        solver = cp_model.CpSolver()
        solver.parameters.enumerate_all_solutions = True
        callback = GroupedSolutionCallback(x, valid_books, bank.id, bank.amount_int, "GROUPED")
        solver.Solve(model, callback)
        candidates.extend(callback.candidates)

    # 3. Many Banks -> 1 Book (Grouped Matches via CP-SAT)
    for book in book_items:
        valid_banks = [b for b in bank_items if (b.direction, book.direction) in DIRECTION_COMPATIBILITY_MAP]
        if not valid_banks:
            continue
            
        model = cp_model.CpModel()
        x = [model.NewBoolVar(f'x_{b.id}') for b in valid_banks]
        
        model.Add(sum(x[i] * valid_banks[i].amount_int for i in range(len(valid_banks))) == book.remaining_amount_int)
        model.Add(sum(x) >= 2)
        
        solver = cp_model.CpSolver()
        solver.parameters.enumerate_all_solutions = True
        callback = GroupedSolutionCallback(x, valid_banks, book.id, book.remaining_amount_int, "GROUPED_BANK")
        solver.Solve(model, callback)
        candidates.extend(callback.candidates)

    if not candidates:
        return "No mathematically obvious candidates found."
        
    return "\n".join(candidates)
