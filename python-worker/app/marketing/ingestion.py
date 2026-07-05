"""
Marketing Ingestion Module

This module provides functionality to parse target prospect data from a CSV file.
It is designed to be invoked by the ASE orchestration engine during the initial
data collection phase of the marketing DAG.
"""
import csv
import json
import os
from typing import List, Dict, Any

def parse_targets(file_path: str = "targets.csv") -> List[Dict[str, Any]]:
    """
    Parses a CSV file containing target prospect information.

    Args:
        file_path (str): The relative or absolute path to the CSV file. Defaults to "targets.csv".

    Returns:
        List[Dict[str, Any]]: A list of dictionaries, where each dictionary represents a valid prospect.
                              Only prospects with a first name, last name, and domain are included.
                              
    Raises:
        FileNotFoundError: If the specified CSV file cannot be located.
    """
    if not os.path.exists(file_path):
        # Fallback to absolute path or just return empty
        base_dir = os.path.dirname(os.path.dirname(os.path.dirname(__file__)))
        file_path = os.path.join(base_dir, "targets.csv")
        if not os.path.exists(file_path):
            raise FileNotFoundError(f"{file_path} not found")
        
    results = []
    with open(file_path, mode='r', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            first_name = row.get("first_name", "").strip()
            last_name = row.get("last_name", "").strip()
            company_name = row.get("company_name", "").strip()
            domain = row.get("domain", "").strip().lower()
            linkedin_url = row.get("linkedin_url", "").strip()
            
            if not all([first_name, last_name, domain]):
                continue
                
            results.append({
                "first_name": first_name,
                "last_name": last_name,
                "company_name": company_name,
                "domain": domain,
                "linkedin_url": linkedin_url
            })
            
    return results
