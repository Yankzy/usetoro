"""
Marketing Permutation Module

This module finds valid corporate email addresses based on a prospect's name 
and company domain. It leverages the `mailscout` package for standardized 
generation and SMTP validation.
"""
import logging
from mailscout import Scout

logger = logging.getLogger(__name__)

# Initialize Scout once to reuse its configuration
scout = Scout()

def generate_permutations(prospect: dict) -> dict:
    """
    Finds valid business emails for a given prospect.

    Args:
        prospect (dict): A dictionary representing a prospect. Expected keys are
                         'first_name', 'last_name', and 'domain'.

    Returns:
        dict: The original prospect dictionary updated with a 'permutations' key
              containing a list of valid email strings.
    """
    first_name = prospect.get("first_name", "").strip()
    last_name = prospect.get("last_name", "").strip()
    domain = prospect.get("domain", "").strip()
    
    if not domain:
        prospect["permutations"] = []
        return prospect
        
    # mailscout accepts a string for a single person's name
    full_name = f"{first_name} {last_name}".strip()
    
    emails = []
    try:
        if full_name:
            emails = scout.find_valid_emails(domain, full_name)
        else:
            # If no names are provided, use brute force on common prefixes
            emails = scout.find_valid_emails(domain)
    except Exception as e:
        logger.error(f"Error using mailscout for {domain}: {e}")
        
    prospect["permutations"] = emails
    return prospect

def find_emails_bulk(prospects: list) -> list:
    """
    Finds valid business emails in bulk for multiple domains and names.
    
    Args:
        prospects (list): A list of dictionaries representing prospects.
    
    Returns:
        list: Results from mailscout bulk finding.
    """
    email_data = []
    for p in prospects:
        domain = p.get("domain", "").strip()
        first_name = p.get("first_name", "").strip()
        last_name = p.get("last_name", "").strip()
        full_name = f"{first_name} {last_name}".strip()
        
        if domain:
            entry = {"domain": domain}
            if full_name:
                entry["names"] = [full_name]
            email_data.append(entry)
            
    if not email_data:
        return []
        
    try:
        return scout.find_valid_emails_bulk(email_data)
    except Exception as e:
        logger.error(f"Error using mailscout bulk: {e}")
        return []

def check_email_deliverability(email: str) -> bool:
    """
    Checks SMTP deliverability of a specific email address.
    """
    try:
        return scout.check_smtp(email)
    except Exception as e:
        logger.error(f"Error checking SMTP for {email}: {e}")
        return False

def check_domain_catchall(domain: str) -> bool:
    """
    Checks if a given domain is configured as a catch-all.
    """
    try:
        return scout.check_email_catchall(domain)
    except Exception as e:
        logger.error(f"Error checking catchall for {domain}: {e}")
        return False

def normalize_prospect_name(name: str) -> str:
    """
    Normalizes a name into an email-friendly format (e.g. stripping diacritics).
    """
    try:
        return scout.normalize_name(name)
    except Exception as e:
        logger.error(f"Error normalizing name {name}: {e}")
        return name

