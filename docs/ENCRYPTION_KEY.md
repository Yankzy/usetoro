# Generate Encryption Key

To use the secret encryption feature, you need to generate a 32-byte encryption key and set it as an environment variable.

## Generating a Key

Run this command to generate a random 32-byte key encoded in base64:

```bash
openssl rand -base64 32
```

Example output:
```
Kv8xZ2rY9Ln4mP3qR5sT7uV0wX1yB2zA3cD4eF5gH6i=
```

## Setting the Environment Variable

Add the generated key to your environment:

```bash
export ENCRYPTION_KEY="9Lf/JTY/gAqAAi4RPoUY3ljWc2aW++m258IjQ/0Y9ZU="
```

Or add it to your `.env` file or deployment configuration.

## Important Notes

- **Key Length**: The key must be exactly 32 bytes when base64-decoded
- **Key Storage**: Store the key securely in a secrets manager (AWS KMS, HashiCorp Vault, etc.) in production
- **Key Rotation**: If the key is compromised, all secrets must be re-encrypted with a new key
- **Backup**: Keep a secure backup of the encryption key - losing it means all encrypted secrets are unrecoverable

## Verifying the Key

The application will validate the encryption key at startup and fail if:
- `ENCRYPTION_KEY` environment variable is not set
- The key is not valid base64
- The decoded key is not exactly 32 bytes
