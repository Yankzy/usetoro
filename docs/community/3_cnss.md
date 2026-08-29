## Service 3 of 5: Free CNSS Employer Compliance Service

This one qualifies because it is not optional. In Morocco, employers must affiliate with the CNSS, register their employees, periodically declare salaries, and pay the corresponding social-security contributions. The government itself states that CNSS affiliation is compulsory and that employers must communicate employee lists, contribution bases, and amounts due. ([Social Development Ministry][1])

So the service should be:

> **Give us your payroll state. We turn it into the legally required CNSS declarations and keep you compliant.**

### A. What the business provides

For each payroll period:

```json
{
  "company": {
    "cnss_number": "...",
    "ice": "..."
  },
  "period": "2026-08",
  "employees": [
    {
      "cnss_number": "...",
      "cin": "...",
      "gross_salary": 8500,
      "days_worked": 26,
      "bonuses": 500,
      "allowances": []
    }
  ]
}
```

The service should also accept payroll exports from Sage, Excel, CSV, ERP systems, or API calls.

---

### B. What we deterministically calculate

The engine maintains versioned CNSS rules:

```text
Contribution bases
Applicable ceilings
Employee contribution
Employer contribution
AMO components
Family allocation components
Other statutory components
Effective dates for every rate
```

Then it calculates:

```json
{
  "employee": "...",
  "contribution_base": 8500,
  "employee_contributions": {...},
  "employer_contributions": {...},
  "total_due": ...
}
```

Every rate must have:

```text
legal source
effective_from
effective_to
ruleset version
```

No LLM.

---

### C. Generate the actual CNSS declaration

The legally required salary declaration should be the output, not merely a payroll report.

The employer is legally required to declare salaries for its employees, and the existing system uses CNSS salary declarations/BDS and DAMANCOM for electronic transmission. ([Readkong][2])

So:

```http
POST /v1/cnss/declarations
```

returns:

```json
{
  "period": "2026-08",
  "status": "VALID",
  "employees_declared": 43,
  "total_contribution": 125840.32,
  "submission_payload": "...",
  "validation_errors": []
}
```

And if CNSS supports the required integration mechanism:

```text
Payroll
   |
   v
CNSS validator
   |
   v
Declaration generator
   |
   v
DAMANCOM / CNSS
   |
   v
Receipt / submission status
```

If direct API submission is unavailable, we still generate the exact legally accepted electronic file so the company uploads it to DAMANCOM.

---

### D. Employee registration

This should also cover another mandatory employer operation.

When someone joins:

```http
POST /v1/cnss/employees
```

```json
{
  "cin": "...",
  "name": "...",
  "start_date": "2026-08-24",
  "existing_cnss_number": null
}
```

The service determines:

```text
Already registered?
    |
    +-- YES: associate existing CNSS identity
    |
    +-- NO: prepare required registration
```

Employers are required to register their employees with the relevant social-security institution. ([Social Development Ministry][1])

So onboarding and offboarding become part of the same public infrastructure.

---

### E. Pre-submission validation

Before sending anything:

```text
Missing CNSS number
Invalid employee identity
Duplicate employee
Impossible number of working days
Negative salary
Contribution base mismatch
Incorrect contribution calculation
Missing newly hired employee
Employee disappeared unexpectedly
Period already declared
```

Return actionable errors:

```json
{
  "code": "MA_CNSS_018",
  "employee": "...",
  "field": "cnss_number",
  "message": "Employee requires CNSS registration before declaration."
}
```

The benefit is avoiding rejected declarations and compliance mistakes before they reach CNSS.

---

### F. Reconciliation with payroll

This is particularly important.

The service should prove:

```text
Payroll employees
        =
CNSS declared employees
```

and:

```text
Payroll contribution calculation
        =
CNSS declaration
```

If not:

```json
{
  "status": "RECONCILIATION_FAILED",
  "differences": [
    {
      "employee": "...",
      "payroll_salary": 9000,
      "declared_salary": 8500,
      "difference": 500
    }
  ]
}
```

This is mandatory-compliance infrastructure, not analytics.

---

### G. Submission evidence

Every declaration should produce an immutable compliance record:

```text
Company
Period
Declaration version
Employees included
Amounts
Submission timestamp
CNSS response
Receipt/reference
Ruleset used
Source payroll fingerprint
```

So three years later, during an audit:

> "Show me exactly what we submitted to CNSS for March 2026."

One API call retrieves it.

---

### H. Corrections

Payroll errors happen.

So declarations must support:

```text
ORIGINAL
   |
   v
CORRECTIVE DECLARATION
```

Never silently overwrite the original.

The service keeps:

```text
what was originally declared
what changed
why it changed
when correction was submitted
result from CNSS
```

---

### I. The free developer layer

Like invoicing, this cannot only be a Toro UI.

Expose:

```text
/v1/cnss/calculate
/v1/cnss/validate
/v1/cnss/declarations
/v1/cnss/employees/register
/v1/cnss/submissions/{id}
/v1/cnss/rules
```

And open libraries:

```text
ma-cnss-rules
ma-cnss-validator
ma-cnss-declaration
```

Now every Moroccan payroll SaaS does not need to independently encode CNSS rules.

---

### J. Why businesses care

This isn't:

> "Here is a nice payroll feature."

It is:

> **"If you employ people, the law requires you to do this anyway."**

A two-person company needs it.

An accounting firm managing 200 clients needs it.

A payroll startup needs it.

A multinational operating in Morocco needs it.

And current products already monetize preparation of the CNSS BDS, which demonstrates there is existing willingness to pay just to handle this compliance plumbing. ([Documentation E-Invoice.ma][3])

We make the basic compliance infrastructure free.

---

### K. The larger pattern is becoming clear

So far:

```text
1. Business transactions
   Enrichment infrastructure

2. Selling
   Legally compliant electronic invoicing

3. Employing people
   Legally compliant CNSS declaration
```

Services #2 and #3 are particularly strong because the business does not have to be persuaded that the underlying activity matters. **The state already requires it.**

For #4, I would stay with that same standard and look at **mandatory DGI tax declarations**, rather than inventing another convenient business tool.

[1]: https://social.gov.ma/questions-frequemment-posees/droits-des-femmes-dans-les-lois-relatives-a-la-securite-sociale/?utm_source=chatgpt.com "Droits des femmes dans les lois relatives à la sécurité sociale - Royaume du Maroc Ministère de la Solidarité, du Développement Social, de l'Egalité et de la Famille"
[2]: https://fr.readkong.com/page/royaume-du-maroc-caisse-nationale-de-securite-sociale-2810354?utm_source=chatgpt.com "ROYAUME DU MAROC CAISSE NATIONALE DE SECURITE SOCIALE - REGIME DE SECURITE SOCIALE RECUEIL DES TEXTES LEGISLATIFS ET REGLEMENTAIRES"
[3]: https://docs.einvoice.ma/fr/payroll/cnss/?utm_source=chatgpt.com "Declarations CNSS (BDS) | Documentation E-Invoice.ma"
