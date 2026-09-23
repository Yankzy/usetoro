from __future__ import annotations

from django.core.management.base import BaseCommand
from bookkeeping_state_eval.dag.parity_company_setup import (
    setup_authoritative_parity_company,
    CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA,
)


class Command(BaseCommand):
    help = "Seeds the authoritative parity test company and Moroccan PCGE Chart of Accounts in Django ledger."

    def add_arguments(self, parser):
        parser.add_argument("--slug", type=str, default="atlas", help="Company slug (default: atlas)")
        parser.add_argument("--name", type=str, default="Atlas Office Solutions SARL", help="Company name")

    def handle(self, *args, **options):
        slug = options["slug"]
        name = options["name"]
        self.stdout.write(f"Setting up authoritative parity company '{name}' (slug: {slug})...")

        entity = setup_authoritative_parity_company(slug=slug, name=name)
        accounts_count = entity.default_coa.accountmodel_set.filter(active=True).not_coa_root().count()

        self.stdout.write(
            self.style.SUCCESS(
                f"Successfully configured authoritative parity company {entity.slug} ({entity.uuid}) "
                f"with default_coa {entity.default_coa.uuid} and {accounts_count} active accounts. "
                f"Telemetry: candidate_source = {CANDIDATE_SOURCE_DJANGO_LEDGER_DEFAULT_COA}"
            )
        )
