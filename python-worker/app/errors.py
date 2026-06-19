class StripeAPIError(Exception):
    def __init__(self, message: str, status_code: int = 502, code: str = "STRIPE_ERROR"):
        super().__init__(message)
        self.message = message
        self.status_code = status_code
        self.code = code


class NATSPublishError(Exception):
    def __init__(self, message: str):
        super().__init__(message)
        self.message = message
