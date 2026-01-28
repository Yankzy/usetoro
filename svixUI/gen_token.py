import hmac
import hashlib
import base64
import json
import time

def generate_token(secret):
    payload = {
        "iss": "svix-server",
        "sub": "org_23rb8YdGqMT0qIzpgGwdXfHirMu",
        "iat": int(time.time()),
        "exp": int(time.time()) + 3600 * 24 * 365 # 1 year
    }
    
    header = {"alg": "HS256", "typ": "JWT"}
    
    def encode(d):
        return base64.urlsafe_b64encode(json.dumps(d).encode()).decode().rstrip("=")
    
    unsigned_token = f"{encode(header)}.{encode(payload)}"
    signature = hmac.new(secret.encode(), unsigned_token.encode(), hashlib.sha256).digest()
    
    return f"{unsigned_token}.{base64.urlsafe_b64encode(signature).decode().rstrip('=')}"

if __name__ == "__main__":
    import os
    from pathlib import Path
    
    # Try to load from .env in core/env or similar, or just take from env var
    # Here we'll just wait for the user to provide or we can try to find it
    secret = os.environ.get("SVIX_JWT_SECRET", "HROjr2Ta05zmKNS0zfu7A7Y5bCPM4KtCNW3MFcRWY0rS3leMxJoUk76xvs65va7JkongjgATngcQLU4")
    print(generate_token(secret))
