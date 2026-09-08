from django.contrib import admin

from ledger.admin.chart_of_accounts import ChartOfAccountsModelAdmin
from ledger.admin.entity import EntityModelAdmin
from ledger.admin.ledger import LedgerModelAdmin
from ledger.models import EntityModel, ChartOfAccountModel, LedgerModel

admin.site.register(EntityModel, EntityModelAdmin)
admin.site.register(ChartOfAccountModel, ChartOfAccountsModelAdmin)
admin.site.register(LedgerModel, LedgerModelAdmin)
