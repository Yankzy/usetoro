"""
Candidate Generation Module for Reconciliation.

This module provides the deterministic, heuristic-based algorithms used to
discover mathematically plausible matches between Bank items and Book items.
By utilizing Bipartite Graph Partitioning and CP-SAT for subset-sum calculations,
it strictly prevents combinatorial explosions on large ledgers.
"""
from typing import List
from reconciliation_prod.domain.bank import BankItem
from reconciliation_prod.domain.book import BookItem
from reconciliation_prod.reconciliation.validation import DIRECTION_COMPATIBILITY_MAP
from ortools.sat.python import cp_model

class GroupedSolutionCallback(cp_model.CpSolverSolutionCallback):
    """
    CP-SAT Callback to collect grouped candidate combinations.
    
    This callback is invoked by the CP-SAT solver every time it finds a 
    valid mathematical subset-sum that matches a target amount. It formats
    the subset into a human-readable string for the Semantic Engine.
    """
    def __init__(self, variables, items, bank_or_book_id, target_amt, prefix, max_solutions=50):
        """
        Initializes the callback with the solver variables and domain context.
        
        Args:
            variables (list): CP-SAT boolean variables representing inclusion.
            items (list): The actual BankItem or BookItem objects corresponding to variables.
            bank_or_book_id (str): The ID of the target item we are matching against.
            target_amt (int): The target amount we are trying to sum to.
            prefix (str): Prefix indicating the type of match (e.g., 'GROUPED', 'GROUPED_BANK').
            max_solutions (int): The maximum number of combinations to collect before stopping.
        """
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
        """
        Invoked by the solver when a valid subset is found.
        Collects the subset and formats it into a string candidate.
        """
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
    Deterministically generates a list of mathematically plausible candidates.
    
    This function applies heuristics (Exact amount, Explicit reference match, Textual overlap)
    to build a bipartite graph. It finds connected components using BFS to isolate dense
    clusters, then uses CP-SAT subset-sum within those clusters to find grouped matches.
    Items with degree 0 (no heuristic links) are grouped into a 'general cluster' with
    tighter subset size caps to prevent combinatorial explosion.

    Args:
        bank_items (List[BankItem]): The list of unresolved bank items.
        book_items (List[BookItem]): The list of open book items.

    Returns:
        str: A formatted string containing a bulleted list of all plausible candidates,
             intended for ingestion by the LLM Semantic Engine.
    """
    import collections
    
    candidates = []
    adj = collections.defaultdict(list)
    
    # 1. Build Bipartite Graph Edges
    for bank in bank_items:
        for book in book_items:
            if (bank.direction, book.direction) not in DIRECTION_COMPATIBILITY_MAP:
                continue
            
            is_edge = False
            
            # Rule 1: Exact Amount match
            if bank.amount_int == book.remaining_amount_int:
                is_edge = True
            # Rule 2: Explicit Reference match
            elif bank.reference and book.reference and bank.reference.lower() == book.reference.lower():
                is_edge = True
            # Rule 3: Textual overlap
            else:
                desc = bank.description.lower()
                if book.counterparty_id and book.counterparty_id.lower() in desc:
                    is_edge = True
                elif book.reference and book.reference.lower() in desc:
                    is_edge = True
                elif book.provenance_refs:
                    for pref in book.provenance_refs:
                        if pref.lower() in desc:
                            is_edge = True
                            break
            
            if is_edge:
                adj[bank.id].append(book.id)
                adj[book.id].append(bank.id)
                
    # 2. Find Connected Components
    visited = set()
    components = []
    general_banks = []
    general_books = []
    
    all_items = {item.id: item for item in bank_items + book_items}
    
    for item in bank_items + book_items:
        if item.id not in visited:
            if not adj[item.id]:
                # Degree 0 -> General Cluster
                if isinstance(item, BankItem):
                    general_banks.append(item)
                else:
                    general_books.append(item)
                visited.add(item.id)
            else:
                # BFS to find component
                comp_banks = []
                comp_books = []
                queue = collections.deque([item.id])
                visited.add(item.id)
                
                while queue:
                    curr_id = queue.popleft()
                    curr = all_items[curr_id]
                    if isinstance(curr, BankItem):
                        comp_banks.append(curr)
                    else:
                        comp_books.append(curr)
                        
                    for nbr in adj[curr_id]:
                        if nbr not in visited:
                            visited.add(nbr)
                            queue.append(nbr)
                            
                components.append((comp_banks, comp_books, False)) # False = not general
                
    if general_banks and general_books:
        components.append((general_banks, general_books, True)) # True = general cluster
        
    # 3. Process each component
    for comp_banks, comp_books, is_general in components:
        max_sols = 3 if is_general else 50
        
        # 3a. Linear scan for 1:1 Matches (Exact and Partial)
        for bank in comp_banks:
            for book in comp_books:
                if (bank.direction, book.direction) not in DIRECTION_COMPATIBILITY_MAP:
                    continue
                bank_amt = bank.amount_int
                book_amt = book.remaining_amount_int
                if bank_amt == book_amt:
                    candidates.append(f"- [EXACT] Bank {bank.id} ({bank_amt}) -> Book {book.id} ({book_amt})")
                elif bank_amt < book_amt:
                    candidates.append(f"- [PARTIAL] Bank {bank.id} ({bank_amt}) -> Book {book.id} (consumes {bank_amt} of {book_amt})")

        # 3b. 1 Bank -> Many Books (Grouped Matches via CP-SAT)
        for bank in comp_banks:
            valid_books = [j for j in comp_books if (bank.direction, j.direction) in DIRECTION_COMPATIBILITY_MAP]
            if not valid_books:
                continue
            model = cp_model.CpModel()
            x = [model.NewBoolVar(f'x_{j.id}') for j in valid_books]
            model.Add(sum(x[i] * valid_books[i].remaining_amount_int for i in range(len(valid_books))) == bank.amount_int)
            model.Add(sum(x) >= 2)
            if is_general:
                model.Add(sum(x) <= 3) # Cap subsets in general pool
                
            solver = cp_model.CpSolver()
            solver.parameters.enumerate_all_solutions = True
            callback = GroupedSolutionCallback(x, valid_books, bank.id, bank.amount_int, "GROUPED", max_solutions=max_sols)
            solver.Solve(model, callback)
            candidates.extend(callback.candidates)

        # 3c. Many Banks -> 1 Book (Grouped Matches via CP-SAT)
        for book in comp_books:
            valid_banks = [b for b in comp_banks if (b.direction, book.direction) in DIRECTION_COMPATIBILITY_MAP]
            if not valid_banks:
                continue
            model = cp_model.CpModel()
            x = [model.NewBoolVar(f'x_{b.id}') for b in valid_banks]
            model.Add(sum(x[i] * valid_banks[i].amount_int for i in range(len(valid_banks))) == book.remaining_amount_int)
            model.Add(sum(x) >= 2)
            if is_general:
                model.Add(sum(x) <= 3) # Cap subsets in general pool
                
            solver = cp_model.CpSolver()
            solver.parameters.enumerate_all_solutions = True
            callback = GroupedSolutionCallback(x, valid_banks, book.id, book.remaining_amount_int, "GROUPED_BANK", max_solutions=max_sols)
            solver.Solve(model, callback)
            candidates.extend(callback.candidates)

    if not candidates:
        return "No mathematically obvious candidates found."
        
    # Deduplicate in case a candidate somehow spans combinations
    unique_candidates = list(dict.fromkeys(candidates))
    return "\n".join(unique_candidates)
