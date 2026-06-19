from pydantic import BaseModel, Field


class CreateCheckoutSessionRequest(BaseModel):
    user_id: str
    amount_in_dollars: float = Field(gt=0)
    product_name: str
    return_url: str
    customer_email: str | None = None


class CreateFCSessionRequest(BaseModel):
    user_id: str


class SyncFCSessionRequest(BaseModel):
    session_id: str = Field(min_length=1)
    user_id: str


class FCCallbackRequest(BaseModel):
    session_id: str
    user_id: str


class CheckoutSessionResponse(BaseModel):
    client_secret: str


class FCSessionResponse(BaseModel):
    client_secret: str
    publishable_key: str


class WebhookResponse(BaseModel):
    status: str


class BankAccountResponse(BaseModel):
    id: str
    stripe_account_id: str
    institution_name: str
    last4: str | None
    subcategory: str | None
    status: str


class CreatePaymentSheetRequest(BaseModel):
    user_id: str
    amount_in_dollars: float


class PaymentSheetResponse(BaseModel):
    clientSecret: str
    ephemeralKey: str
    customer: str
