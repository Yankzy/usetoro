# 1. Target Customer Profiles (ICP)

Focus on companies or teams that:

### A) Are **developer-centric** with integrations exposure

Their product is defined by integrations with third-party services, e.g. payments, CRM, marketing, comms.

**Examples**

* SaaS platforms with multiple external providers
* Embedded fintech apps
* Developer tools
* Marketplaces
* B2B platforms

These teams care about webhook reliability because breakage costs them revenue and engineering time.

---

### B) Operate in **event-driven business flows**

Webhooks are core because events trigger core product features.

**Example categories**

* Payments & billing (Stripe, Adyen, PayPal)
* Messaging & notifications (Slack, Twilio)
* E-commerce (Shopify, BigCommerce)
* Identity & auth (Okta, Auth0)
* Helpdesk & support (Zendesk, Freshdesk)
* CI/CD & workflow tools (GitHub, GitLab)
* Accounting & finance (Xero, QuickBooks)

These companies already struggle or will struggle with operational webhook plumbing.

---

### C) Have a real **production volume**

You want webhook traffic — not hobby projects.

Good signals:

* B2B SaaS
* Enterprise or mid-market customers
* Funding or growth signals (Series A+ or steady revenue)

---

# 2. Real Companies to Target (Webhook-Heavy)

Below is a **ready-to-reach list**. Start with the first group, they are most likely to care:

## A) Payments & Billing

* **Stripe customers who build on Stripe** (not Stripe itself — target their customers):

  * **Ramp**
  * **Teachable**
  * **Gumroad**
  * **Memberstack**
  * **Chargebee**
* **Payment orchestration and billing tools**

  * **Recurly**
  * **Stripe Atlas [partners](https://stripe.partners/?f_help-me-with=platforms-that-embed-stripe&f_stripe-solution=online-payments&f_category=accounting)** 
  * **PayMongo (in SEA)**

## B) SaaS with Heavy Integrations

* **Zapier competitors or adjacent**

  * **Make (Integromat)**
* **Customer engagement / automation**

  * **Intercom**
  * **Segment (Twilio)**
  * **Iterable**
  * **Customer.io**

## C) E-commerce / Marketplaces

* **Shopify apps** (especially those doing fulfillment, inventory sync)

  * **Bold Commerce**
  * **Klaviyo**
  * **Glew.io**
* **Multi-marketplace tools**

  * **ChannelAdvisor**
  * **Feedonomics**

## D) Developer Ecosystem Tools

* **Sentry**
* **PagerDuty**
* **Linear**
* **Clubhouse (Shortcut)**

## E) Helpdesk & Support

* **Zendesk App partners**
* **Freshworks partners**
* **Help Scout integrations**
* **Front**

## F) Identity / Auth

* **Auth0 ecosystem apps**
* **Okta partners**
* **Magic.link partners**

---

# 3. Outreach Criteria (Who Is Best)

Priority if one or more are true:

✅ They integrate multiple webhook sources
✅ They rely on event streams for core product logic
✅ They operate in B2B SaaS or developer product
✅ They have engineering teams doing gluing work
✅ They pay engineers to maintain reliability code

Less ideal (later):

* Companies using only one webhook
* Internal tools teams without external API consumers
* Products with no external integrations

---

# 4. Prospecting Script — Cold Outreach (Concise, Non-Hype)

This is a short email you can send Monday.

**Subject:**
Quick question about how you handle webhook infrastructure

**Body (email):**

```
Hi {{FirstName}},

I’m building a lightweight webhook ingestion & normalization service that securely receives, cleans, and forwards webhook events to your endpoints or database with replay and delivery guarantees.

We’re in final development and testing — before we launch I want to validate a few product assumptions with teams like yours that are heavy on integrations.

Right now our service:
• Receives high volume webhooks (1,000+ per second)
• Normalizes event payloads into consistent formats
• Forwards to your API, DB, or any HTTP endpoint reliably
• Provides logs and visibility into delivered events

I’d love 10–15 minutes to hear:
1) How you currently handle inbound webhooks
2) Biggest pain points you’ve run into
3) One feature that would make webhook infrastructure effortless

Are you available for a quick call this week?  

Thanks,  
{{YourName}}  
{{Company / Link to UI preview if available}}
```

---

# 5. Short Version — DM / Slack / LinkedIn

```
Hey {{FirstName}}, quick question — how are you handling webhook ingestion & retries for [Stripe / Slack / Shopify] events today? Building a reliable, normalized webhook layer and gathering early feedback from teams like yours. 10 min call?
```

---

# 6. Follow-Up Sequence (Short)

**Follow-Up 1 (48 hrs later):**

```
Hi {{FirstName}}, just bumping this — a quick chat would help me understand if webhook reliability is a real pain for your team or not. Thanks!
```

**Follow-Up 2 (96 hrs later):**

```
Hi {{FirstName}}, no worries if this isn’t a priority — if you ever find yourself debugging webhooks or replaying events, feel free to reach out. We’re building something to simplify that.
```

---

# 7. What to Ask on Early Calls

Ask **three qualifiers**:

1. **Volume**
   “Roughly how many webhook events do you process per minute/hour?”

2. **Pain**
   “What’s the worst webhook issue you’ve dealt with in the last quarter?”

3. **Cost**
   “How many engineer hours per month go into webhook handling or retries?”

Their answers directly map to pricing later.

---

## 8. How to Qualify into Price Bands Quickly

**Low pain + low volume**
→ $99–$249/mo — low engagement.

**Moderate pain + daily disruptions**
→ $499–$1,500/mo — they have real costs.

**Mission-critical use (billing, revenue, compliance)**
→ $1,500–$5,000/mo — this is where real value (and budgets) live.

---

If you want next, I can give you:

* A **LinkedIn connection script**
* A **call script** with objection handling
* A **qualification scoring table** to assign prices based on their answers

Just tell me **which one first**.
