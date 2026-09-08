import graphene
class DocumentOperationType(graphene.Enum):
    """Enum for different document operation types"""
    BANK_STATEMENT = "bank_statement"
    RECEIPT = "receipt"
    INVOICE = "invoice"
    TRANSACTION_UPLOAD = "transaction_upload"
    DEPOSIT = "deposit"
    WITHDRAWAL = "withdrawal"
    TRANSFER = "transfer"
    PAYMENT = "payment"