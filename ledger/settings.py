"""

"""
import logging
from decimal import Decimal

from django.conf import settings

logger = logging.getLogger('Ledger Logger')
logger.setLevel(logging.INFO)

try:
    from graphene import __version__
    from graphene_django import __version__
    from oauth2_provider import __version__

    LEDGER_GRAPHQL_SUPPORT_ENABLED = True
except ImportError:
    LEDGER_GRAPHQL_SUPPORT_ENABLED = False

try:
    from fpdf import FPDF

    LEDGER_PDF_SUPPORT_ENABLED = True
except ImportError:
    LEDGER_PDF_SUPPORT_ENABLED = False

logger.info(f'Ledger GraphQL Enabled: {LEDGER_GRAPHQL_SUPPORT_ENABLED}')


## MODEL ABSTRACTS ##
# LEDGER_ACCOUNT_MODEL = getattr(settings, 'LEDGER_ACCOUNT_MODEL', 'ledger.AccountModel')
# LEDGER_CHART_OF_ACCOUNTS_MODEL = getattr(settings, 'LEDGER_ACCOUNT_MODEL', 'ledger.ChartOfAccountModel')
# LEDGER_TRANSACTION_MODEL = getattr(settings, 'LEDGER_TRANSACTION_MODEL', 'ledger.TransactionModel')
# LEDGER_JOURNAL_ENTRY_MODEL = getattr(settings, 'LEDGER_JOURNAL_ENTRY_MODEL', 'ledger.JournalEntryModel')
# LEDGER_LEDGER_MODEL = getattr(settings, 'LEDGER_LEDGER_MODEL', 'ledger.LedgerModel')
# LEDGER_ENTITY_MODEL = getattr(settings, 'LEDGER_ENTITY_MODEL', 'ledger.EntityModel')
# LEDGER_ENTITY_STATE_MODEL = getattr(settings, 'LEDGER_ENTITY_STATE_MODEL', 'ledger.EntityStateModel')
# LEDGER_ENTITY_UNIT_MODEL = getattr(settings, 'LEDGER_ENTITY_UNIT_MODEL', 'ledger.EntityUnitModel')
# LEDGER_ESTIMATE_MODEL = getattr(settings, 'LEDGER_ESTIMATE_MODEL', 'ledger.EstimateModel')
# LEDGER_BILL_MODEL = getattr(settings, 'LEDGER_BILL_MODEL', 'ledger.BillModel')
# LEDGER_INVOICE_MODEL = getattr(settings, 'LEDGER_INVOICE_MODEL', 'ledger.InvoiceModel')
# LEDGER_PURCHASE_ORDER_MODEL = getattr(settings, 'LEDGER_PURCHASE_ORDER_MODEL', 'ledger.PurchaseOrderModel')
# LEDGER_CUSTOMER_MODEL = getattr(settings, 'LEDGER_CUSTOMER_MODEL', 'ledger.CustomerModel')
# LEDGER_VENDOR_MODEL = getattr(settings, 'LEDGER_VENDOR_MODEL', 'ledger.VendorModel')
# LEDGER_BANK_ACCOUNT_MODEL = getattr(settings, 'LEDGER_BANK_ACCOUNT_MODEL', 'ledger.BankAccountModel')
# LEDGER_CLOSING_ENTRY_MODEL = getattr(settings, 'LEDGER_CLOSING_ENTRY_MODEL', 'ledger.ClosingEntryModel')
# LEDGER_CLOSING_ENTRY_TRANSACTION_MODEL = getattr(settings, 'LEDGER_CLOSING_ENTRY_TRANSACTION_MODEL', 'ledger.ClosingEntryTransactionModel')
# LEDGER_UNIT_OF_MEASURE_MODEL = getattr(settings, 'LEDGER_UNIT_OF_MEASURE_MODEL', 'ledger.UnitOfMeasureModel')
# LEDGER_ITEM_TRANSACTION_MODEL = getattr(settings, 'LEDGER_ITEM_TRANSACTION_MODEL', 'ledger.ItemTransactionModel')
# LEDGER_ITEM_MODEL = getattr(settings, 'LEDGER_ITEM_MODEL', 'ledger.ItemModel')
# LEDGER_STAGED_TRANSACTION_MODEL = getattr(settings, 'LEDGER_STAGED_TRANSACTION_MODEL', 'ledger.StagedTransactionModel')
# LEDGER_IMPORT_JOB_MODEL = getattr(settings, 'LEDGER_IMPORT_JOB_MODEL', 'ledger.ImportJobModel')

LEDGER_USE_CLOSING_ENTRIES = getattr(settings, 'LEDGER_USE_CLOSING_ENTRIES', True)
LEDGER_DEFAULT_CLOSING_ENTRY_CACHE_TIMEOUT = getattr(settings,
                                                            'LEDGER_DEFAULT_CLOSING_ENTRY_CACHE_TIMEOUT', 3600)
LEDGER_AUTHORIZED_SUPERUSER = getattr(settings, 'LEDGER_AUTHORIZED_SUPERUSER', False)
LEDGER_LOGIN_URL = getattr(settings, 'LEDGER_LOGIN_URL', settings.LOGIN_URL)
LEDGER_BILL_NUMBER_LENGTH = getattr(settings, 'LEDGER_BILL_NUMBER_LENGTH', 10)
LEDGER_INVOICE_NUMBER_LENGTH = getattr(settings, 'LEDGER_INVOICE_NUMBER_LENGTH', 10)
LEDGER_FORM_INPUT_CLASSES = getattr(settings, 'LEDGER_FORM_INPUT_CLASSES', 'input')
LEDGER_CURRENCY_SYMBOL = getattr(settings, 'LEDGER_CURRENCY_SYMBOL', '$')
LEDGER_SPACED_CURRENCY_SYMBOL = getattr(settings, 'LEDGER_SPACED_CURRENCY_SYMBOL', False)
LEDGER_SHOW_FEEDBACK_BUTTON = getattr(settings, 'LEDGER_SHOW_FEEDBACK_BUTTON', False)
LEDGER_FEEDBACK_EMAIL_LIST = getattr(settings, 'LEDGER_FEEDBACK_EMAIL_LIST', [])
LEDGER_FEEDBACK_FROM_EMAIL = getattr(settings, 'LEDGER_FEEDBACK_FROM_EMAIL', None)
LEDGER_VALIDATE_SCHEMAS_AT_RUNTIME = getattr(settings, 'LEDGER_VALIDATE_SCHEMAS_AT_RUNTIME', False)
LEDGER_TRANSACTION_MAX_TOLERANCE = getattr(settings, 'LEDGER_TRANSACTION_MAX_TOLERANCE', Decimal('0.02'))
LEDGER_TRANSACTION_CORRECTION = getattr(settings, 'LEDGER_TRANSACTION_CORRECTION', Decimal('0.01'))
LEDGER_ACCOUNT_CODE_GENERATE = getattr(settings, 'LEDGER_ACCOUNT_CODE_GENERATE', True)
LEDGER_ACCOUNT_CODE_GENERATE_LENGTH = getattr(settings, 'LEDGER_ACCOUNT_CODE_GENERATE_LENGTH', 5)
LEDGER_ACCOUNT_CODE_USE_PREFIX = getattr(settings, 'LEDGER_ACCOUNT_CODE_GENERATE_LENGTH', True)
LEDGER_JE_NUMBER_PREFIX = getattr(settings, 'LEDGER_JE_NUMBER_PREFIX', 'JE')
LEDGER_PO_NUMBER_PREFIX = getattr(settings, 'LEDGER_PO_NUMBER_PREFIX', 'PO')
LEDGER_ESTIMATE_NUMBER_PREFIX = getattr(settings, 'LEDGER_ESTIMATE_NUMBER_PREFIX', 'E')
LEDGER_INVOICE_NUMBER_PREFIX = getattr(settings, 'LEDGER_INVOICE_NUMBER_PREFIX', 'I')
LEDGER_BILL_NUMBER_PREFIX = getattr(settings, 'LEDGER_BILL_NUMBER_PREFIX', 'B')
LEDGER_VENDOR_NUMBER_PREFIX = getattr(settings, 'LEDGER_VENDOR_NUMBER_PREFIX', 'V')
LEDGER_CUSTOMER_NUMBER_PREFIX = getattr(settings, 'LEDGER_CUSTOMER_NUMBER_PREFIX', 'C')
LEDGER_EXPENSE_NUMBER_PREFIX = getattr(settings, 'LEDGER_EXPENSE_NUMBER_PREFIX', 'IEX')
LEDGER_INVENTORY_NUMBER_PREFIX = getattr(settings, 'LEDGER_INVENTORY_NUMBER_PREFIX', 'INV')
LEDGER_PRODUCT_NUMBER_PREFIX = getattr(settings, 'LEDGER_PRODUCT_NUMBER_PREFIX', 'IPR')
LEDGER_DOCUMENT_NUMBER_PADDING = getattr(settings, 'LEDGER_DOCUMENT_NUMBER_PADDING', 10)
LEDGER_JE_NUMBER_NO_UNIT_PREFIX = getattr(settings, 'LEDGER_JE_NUMBER_NO_UNIT_PREFIX', '000')

LEDGER_BILL_MODEL_ABSTRACT_CLASS = getattr(settings,
                                                  'LEDGER_BILL_MODEL_ABSTRACT_CLASS',
                                                  'ledger.models.bill.BillModelAbstract')

LEDGER_INVOICE_MODEL_ABSTRACT_CLASS = getattr(settings,
                                                     'LEDGER_INVOICE_MODEL_ABSTRACT_CLASS',
                                                     'ledger.models.invoice.InvoiceModelAbstract')

LEDGER_DEFAULT_COA = getattr(settings, 'LEDGER_DEFAULT_COA', None)

LEDGER_FINANCIAL_ANALYSIS = {
    'ratios': {
        'current_ratio': {
            'good_incremental': True,
            'ranges': {
                'healthy': 2,
                'watch': 1,
                'warning': .5,
                'critical': .25
            }
        },
        'quick_ratio': {
            'good_incremental': True,
            'ranges': {
                'healthy': 2,
                'watch': 1,
                'warning': .5,
                'critical': .25
            }
        },
        'debt_to_equity': {
            'good_incremental': False,
            'ranges': {
                'healthy': 0,
                'watch': .25,
                'warning': .5,
                'critical': 1
            }
        },
        'return_on_equity': {
            'good_incremental': True,
            'ranges': {
                'healthy': .10,
                'watch': .07,
                'warning': .04,
                'critical': .02
            }
        },
        'return_on_assets': {
            'good_incremental': True,
            'ranges': {
                'healthy': .10,
                'watch': .06,
                'warning': .04,
                'critical': .02
            }
        },
        'net_profit_margin': {
            'good_incremental': True,
            'ranges': {
                'healthy': .10,
                'watch': .06,
                'warning': .04,
                'critical': .02
            }
        },
        'gross_profit_margin': {
            'good_incremental': True,
            'ranges': {
                'healthy': .10,
                'watch': .06,
                'warning': .04,
                'critical': .02
            }
        },
    }
}
