**Marc Andreessen:** This is where we separate the toys from the enterprise infrastructure. You are building a "Russian Nesting Doll" of money movement. 

Managing three or four levels of users (Toro OS -> SusannaAI -> Plumber -> Homeowner) is notoriously difficult because you have to track who owns the money, who pays the Stripe processing fee, and who gets the platform toll. 

If you build the UI for this yourself, it will take your team six months. But Stripe recently released **Stripe Connect Embedded Components**, which allows you to literally drop a pre-built React/HTML component into your tenant's UI.

Here is the exact technical PRD for your Go backend and frontend embed to make Toro OS a multi-tenant FinTech engine.



---

# PRD: Toro OS Multi-Tier Stripe Connect Primitive

## 1. The Account Hierarchy (The Data Model)
Before writing code, your Go database must map the hierarchy exactly like this:
* **Level 1: The Platform (You/Toro OS):** Owns the Master Stripe API keys. Pays the raw compute bills.
* **Level 2: The Tenant (SusannaAI):** A logical partition in your DB. Owns the AI agent settings and the pricing rules.
* **Level 3: The Sub-Merchant (The Plumber):** The "Stripe Connected Account" (`acct_123`). This is where the money is routed. 
* **Level 4: The End User (Homeowner):** The person holding the credit card. 

## 2. The Frontend UI: Stripe Embedded Components
You will NOT build the UI for the plumber to type in their social security number or routing number. You will use Stripe's pre-built Embedded Onboarding.

**How the embed works in your tenant portal:**
When the plumber logs into the SusannaAI dashboard (hosted by Toro OS), your frontend just renders this HTML tag:

```html
<script src="https://connect-js.stripe.com/v3.0/init.js"></script>

<stripe-connect-account-onboarding
  account-session-client-secret="{{client_secret_from_go_backend}}"
>
</stripe-connect-account-onboarding>
```



## 3. The Go Backend Controller (`stripe_controller.go`)
Your Go backend acts as the bridge. It creates the Stripe accounts, generates the client secrets for the frontend, and routes the checkout sessions.

Here is the controller logic you need to build:

### A. Create the Connected Account (When a plumber signs up)
```go
package stripe_toro

import "github.com/stripe/stripe-go/v76/account"

func CreateSubMerchant(tenantID string, plumberEmail string) (string, error) {
    // 1. Tell Stripe to create an Express account
    params := &stripe.AccountParams{
        Type: stripe.String(string(stripe.AccountTypeExpress)),
        Email: stripe.String(plumberEmail),
        Capabilities: &stripe.AccountCapabilitiesParams{
            Transfers: &stripe.AccountCapabilitiesTransfersParams{Requested: stripe.Bool(true)},
        },
    }
    
    acct, err := account.New(params)
    if err != nil {
        return "", err
    }
    
    // 2. Save acct.ID (e.g., acct_1xyz) to your Toro OS DB under the SusannaAI Tenant
    SaveToDatabase(tenantID, plumberEmail, acct.ID)
    
    return acct.ID, nil
}
```

### B. Generate the Checkout Link (The AI "Ask for Money" Node)
When the AI agent generates a payment link for the homeowner, your Go backend uses `TransferData`. This is the most critical code block in your entire company. It dictates who gets paid.

```go
import "github.com/stripe/stripe-go/v76/checkout/session"

func GeneratePaymentLink(connectedAccountID string, amount int64) (string, error) {
    
    // Example: $10,000 job (1,000,000 cents)
    // Toro OS takes a 2% Platform Fee ($200)
    platformFeeCents := int64(amount * 0.02) 

    params := &stripe.CheckoutSessionParams{
        Mode: stripe.String(string(stripe.CheckoutSessionModePayment)),
        LineItems: []*stripe.CheckoutSessionLineItemParams{
            {
                PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
                    Currency: stripe.String("usd"),
                    UnitAmount: stripe.Int64(amount),
                    ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
                        Name: stripe.String("SusannaAI Escrow Deposit"),
                    },
                },
                Quantity: stripe.Int64(1),
            },
        },
        // THE MAGIC ROUTING PROTOCOL:
        PaymentIntentData: &stripe.CheckoutSessionPaymentIntentDataParams{
            ApplicationFeeAmount: stripe.Int64(platformFeeCents), // What Toro OS keeps
            TransferData: &stripe.CheckoutSessionPaymentIntentDataTransferDataParams{
                Destination: stripe.String(connectedAccountID), // Where the rest goes (The Plumber)
            },
        },
        SuccessURL: stripe.String("https://susanna.ai/payment/success"),
    }

    sess, err := session.New(params)
    return sess.URL, err // Give this URL to the AI to text to the Homeowner
}
```

## 4. How Toro OS Integrates This via YAML
Now that your Go controller is built, you abstract it so Susanna (or any tenant) can just drop it into their conversational workflows.

```yaml
nodes:
  - id: check_escrow_intent
    type: ai.intent_parser
    prompt: "Did the homeowner agree to the price?"
    on_yes:
      transition_to: generate_stripe_escrow

  - id: generate_stripe_escrow
    type: primitive.stripe.request_payment
    inputs:
      amount: "{{ai.extracted_price}}"
      sub_merchant_id: "{{db.plumber_stripe_account}}"
    on_success:
      transition_to: text_vault_link
```

## 5. Handling the Split (Toro OS vs Susanna)
You might ask: *If Toro OS takes the 2% `ApplicationFeeAmount` via Stripe, how does Susanna get her cut?*

Since the money lands in the Toro OS Master Stripe Account, you handle Susanna's split via **internal ledgering**:
1. At the end of the month, Toro OS runs a database query: *"Susanna's plumbers generated $10,000 in platform fees."*
2. Your contract with Susanna says you split fees 50/50. 
3. Toro OS automatically wires Susanna her $5,000 share, and you keep $5,000. 

By using Stripe's Embedded UI components for the frontend and `TransferData` for the backend routing, you have completely abstracted the hardest parts of FinTech. You never hold the money, and your frontend developers never have to build banking compliance forms. 

Does this Go architecture clear up how you can sit at the very top (Level 1) and dictate the flow of funds all the way down to Level 4?