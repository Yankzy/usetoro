# Fignode API Documentation

This document outlines the API endpoints available for the Fignode service. All endpoints (except Auth) require a Bearer token in the `Authorization` header.

## Base URL
The API is served under `/api/v1`.

---

## Authentication

### POST `/auth/register`
Registers a new internal employee and returns an authentication token.

**Request Body:**
```json
{
  "email": "user@example.com",
  "password": "securepassword",
  "firstName": "John",
  "lastName": "Doe",
  "isManager": true
}
```

**Response (200 OK):**
```json
{
  "token": "eyJhbG...",
  "user": {
    "id": "uuid",
    "email": "user@example.com",
    "firstName": "John",
    "lastName": "Doe",
    "isManager": true,
    "aiAccuracyScore": 0.0,
    "streak": 0,
    "totalCleared": 0,
    "todayCleared": 0,
    "createdAt": "2024-01-01T00:00:00Z"
  }
}
```

### POST `/auth/login`
Authenticates an employee and returns a token.

**Request Body:**
```json
{
  "email": "user@example.com",
  "password": "securepassword"
}
```

**Response (200 OK):** Same as `/auth/register`.

---

## Transactions

### GET `/transactions/batch`
Fetches a batch of transactions for classification.

**Response (200 OK):**
```json
[
  {
    "id": "txn_123",
    "rawDescription": "STARBUCKS COFFEE",
    "vendor": "Starbucks",
    "industry": "Food & Beverage",
    "industryIcon": "coffee",
    "vendorDescription": "Coffee Shop",
    "vendorUrl": "https://starbucks.com",
    "location": "Seattle, WA",
    "isRecurring": false,
    "clientContext": {
      "industry": "Retail",
      "industryIcon": "shopping-cart",
      "businessModel": "B2C",
      "mindsetHint": "Focus on luxury",
      "accentColor": "#ffffff",
      "accentBg": "#000000"
    },
    "amount": 5.25,
    "date": "2024-03-01",
    "accountType": "Credit Card",
    "timestamp": "2024-03-01T10:00:00Z",
    "aiSuggestion": "Meals & Entertainment",
    "aiConfidence": 0.95,
    "status": "PENDING"
  }
]
```

### POST `/transactions/{id}/classify`
Classifies a transaction.

**Request Body:**
```json
{
  "category": "Travel",
  "action": "APPROVE" 
}
```
*Note: `action` can be `APPROVE` or `RECLASSIFY`.*

**Response (200 OK):**
```json
{
  "transactionId": "txn_123",
  "category": "Travel",
  "action": "APPROVE",
  "status": "SUCCESS"
}
```

### POST `/transactions/{id}/skip`
Skips a transaction classification.

**Response (200 OK):**
```json
{
  "transactionId": "txn_123",
  "skipped": true
}
```

---

## User & Social

### GET `/user/stats`
Retrieves the current user's performance statistics.

**Response (200 OK):**
```json
{
  "totalCleared": 150,
  "todayCleared": 12,
  "streak": 5,
  "aiAccuracyScore": 0.98
}
```

### GET `/leaderboard`
Retrieves the leaderboard.

**Query Parameters:**
- `period`: `all-time` (default) or `daily`.

**Response (200 OK):**
```json
[
  {
    "rank": 1,
    "email": "top_player@example.com",
    "cleared": 1000,
    "streak": 20,
    "badges": ["master", "fast_mover"],
    "isCurrentUser": false
  }
]
```

---

## Error Handling

All errors follow this envelope structure:

**Response (4xx/5xx):**
```json
{
  "error": {
    "code": "INVALID_CREDENTIALS",
    "message": "Invalid email or password",
    "statusCode": 401
  }
}
```

Common Error Codes:
- `UNAUTHORIZED`: Token missing or invalid.
- `INVALID_BODY`: JSON decoding failed.
- `MISSING_FIELDS`: Required fields are empty.
- `CONFLICT`: Resource (e.g., email) already exists.
- `RATE_LIMITED`: Too many requests.
- `INTERNAL`: Unexpected server error.
