# ledger/gql/entity/types.py

import graphene
from graphene_django import DjangoObjectType
from ledger.models.entity import EntityModel
from ledger.gql.bill.types import BillModelNode
from ledger.gql.invoice.types import InvoiceModelNode
from django.db.models import Field


class PnLSummaryType(graphene.ObjectType):
    revenue = graphene.Float()
    expenses = graphene.Float()
    gross_profit = graphene.Float()
    net_income = graphene.Float()

class EntityModelNode(DjangoObjectType):
    unpaid_bills = graphene.List(lambda: BillModelNode, description="Unpaid bills for this entity")
    unpaid_invoices = graphene.List(lambda: InvoiceModelNode, description="Unpaid invoices for this entity")
    pnl_summary = graphene.Field(
        PnLSummaryType,
        date=graphene.Date(),
        from_date=graphene.Date(),
        to_date=graphene.Date(),
        description="P&L summary for this entity and date or date range"
    )
    net_payables = graphene.Float(description="Net payables for this entity")
    net_receivables = graphene.Float(description="Net receivables for this entity")

    class Meta:
        model = EntityModel
        interfaces = (graphene.relay.Node,)
        filter_fields = {
            'uuid': ['exact'],
            'name': ['icontains'],
            'slug': ['exact'],
            'admin': ['exact'],
            'admin__email': ['exact', 'icontains'],
            'managers': ['exact'],
            'default_coa': ['exact'],
            'accrual_method': ['exact'],
            'fy_start_month': ['exact', 'gte', 'lte'],
            'last_closing_date': ['exact', 'gte', 'lte'],
            'hidden': ['exact'],
            'address_1': ['icontains'],
            'address_2': ['icontains'],
            'city': ['icontains'],
            'state': ['icontains'],
            'zip_code': ['icontains'],
            'country': ['icontains'],
            'email': ['icontains'],
            'website': ['icontains'],
            'phone': ['exact', 'icontains'],
            'created': ['exact', 'gte', 'lte'],
            'updated': ['exact', 'gte', 'lte'],
        }

        exclude = (
            'chartofaccountmodel_set', 
            'meta', 
            'chartofaccountmodel_set',
            'customermodel_set',
            'depth',
            'ledgermodel_set',
            'managers',
            'numchild',
            'path',
        )

    def resolve_unpaid_bills(self, info):
        return self.get_bills().filter(is_paid=False)

    def resolve_unpaid_invoices(self, info):
        return self.get_invoices().filter(is_paid=False)

    
    def resolve_pnl_summary(self, info, date=None, from_date=None, to_date=None):
        summary = {}
        if hasattr(self, "get_pnl_summary"):
            if date:
                summary = self.get_pnl_summary(date=date)
            elif from_date and to_date:
                summary = self.get_pnl_summary(from_date=from_date, to_date=to_date)
        return PnLSummaryType(
            revenue=summary.get("revenue", 0.0),
            expenses=summary.get("expenses", 0.0),
            gross_profit=summary.get("gross_profit", 0.0),
            net_income=summary.get("net_income", 0.0),
        )

    def resolve_net_payables(self, info):
        return self.get_net_payables() if hasattr(self, "get_net_payables") else 0.0

    def resolve_net_receivables(self, info):
        return self.get_net_receivables() if hasattr(self, "get_net_receivables") else 0.0
    

    def resolve_pnl_summary(self, info):
        # Replace with your actual summary logic
        summary = self.get_pnl_summary() if hasattr(self, "get_pnl_summary") else {}
        return PnLSummaryType(
            revenue=summary.get("revenue", 0.0),
            expenses=summary.get("expenses", 0.0),
            gross_profit=summary.get("gross_profit", 0.0),
            net_income=summary.get("net_income", 0.0),
            # Add more fields as needed
        )