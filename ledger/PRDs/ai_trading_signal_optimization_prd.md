# **Technical PRD: AI Trading Signal and Portfolio Optimization Engine**

**Alpha Vantage \+ LLM Semantic Signals \+ Quantitative Risk Models \+ Mathematical Optimization**

Version 1.0 | August 2026

Purpose: Define a chronological, implementation-ready architecture for converting market data and unstructured market intelligence into auditable trading signals, then converting those signals into mathematically constrained portfolio recommendations. The design deliberately separates interpretation, statistical estimation, optimization, explanation, and execution.

Audience: Founder/product owner first, then an implementation LLM or engineering team. The document therefore explains the reasoning behind each mathematical component before defining the formal model and implementation requirements.

Important scope boundary: This system generates recommendations and proposed trades. Broker execution through Alpaca must remain a distinct, permissioned stage with explicit risk controls and user authorization. The optimization engine must never be bypassed by an LLM-generated allocation.

# **1\. Product thesis and design principle**

The current implementation pattern of placing the entire portfolio and Alpha Vantage intelligence into an LLM context and asking the model what to buy or sell is conceptually simple, but it makes the LLM responsible for tasks it is not well suited to perform reliably. The model is simultaneously interpreting news, estimating expected returns, comparing risk, understanding correlations, respecting portfolio limits, sizing positions, and explaining the result. Those are different mathematical problems and should be separated.

The proposed engine treats the LLM as a semantic measurement instrument, not as the optimizer. It extracts structured meaning from unstructured evidence. Statistical models estimate return distributions and portfolio risk. A mathematical optimizer then computes the best feasible portfolio under explicit constraints. The LLM returns at the end to explain the mathematical result in language.

Market evidence \-\> semantic/quantitative signals \-\> calibrated forecasts \-\> risk model \-\> optimization \-\> proposed trades \-\> explanation \-\> approval \-\> Alpaca execution

This separation creates three properties that are difficult to obtain from a prompt-only architecture: reproducibility, backtestability, and constraint guarantees. If a recommendation changes, the system can identify whether the cause was new evidence, a changed forecast, a changed covariance estimate, or a binding portfolio constraint.

# **2\. Chronological execution pipeline**

Every recommendation cycle follows the same order. This order is part of the product contract. Later stages may consume outputs from earlier stages, but an LLM may not skip directly from raw evidence to a trade instruction.

| Stage | Component | Result |
| :---- | :---- | :---- |
| Stage 0 | Load portfolio and policy state | Current positions, cash, user risk profile, asset permissions, concentration rules, tax/turnover preferences. |
| Stage 1 | Acquire market evidence | Alpha Vantage time series, technical indicators, fundamentals, news and sentiment, plus any approved market feeds. |
| Stage 2 | Normalize and align data | Map symbols, timestamps, currencies, trading calendars, corporate actions, missing values, and evidence provenance. |
| Stage 3 | Compute deterministic quantitative features | Returns, momentum, volatility, drawdown, liquidity features, factor/sector exposures, technical features, covariance inputs. |
| Stage 4 | Extract LLM semantic signals | Convert news, earnings narratives, filings, and other text into bounded structured scores with evidence citations and confidence. |
| Stage 5 | Calibrate and combine signals | Transform heterogeneous signals into comparable forecast units. Learn historical weights from out-of-sample performance. |
| Stage 6 | Estimate expected returns and uncertainty | Produce expected return vector mu, uncertainty intervals, and scenario distributions. No allocation occurs yet. |
| Stage 7 | Estimate portfolio risk | Build covariance matrix Sigma and optional downside-risk/scenario models. |
| Stage 8 | Solve target allocation | Use convex/quadratic optimization to choose mathematically optimal portfolio weights under continuous constraints. |
| Stage 9 | Solve executable rebalance | If discrete trade rules matter, use mixed-integer optimization for minimum trade size, trade count, lots, turnover, and other execution constraints. |
| Stage 10 | Validate and stress test | Reject unstable, infeasible, overconfident, or policy-violating solutions. Run scenario and sensitivity checks. |
| Stage 11 | Generate explanation | LLM explains proposed changes using optimizer outputs, binding constraints, and evidence. It may not change the solution. |
| Stage 12 | Approval and broker execution | After permission checks and approval policy, translate proposed trades into Alpaca orders and monitor fills. |

# **3\. Stage 0-2: portfolio state and Alpha Vantage evidence**

The engine begins with the portfolio, not the market feed. An optimizer cannot produce a meaningful rebalance unless it knows what is already owned, available cash, the investor policy, and what kinds of changes are permissible.

For each asset i at decision time t, define current market value h\_i,t, current portfolio weight w\_i,t \= h\_i,t / V\_t, where V\_t is total portfolio value including cash. Persist acquisition cost and tax-lot information if future versions will optimize realized gains or taxes.

Alpha Vantage should be treated as evidence infrastructure. Its current documentation exposes adjusted market time series, technical indicators, fundamentals, and Alpha Intelligence news and sentiment feeds. Technical indicators are derived from adjusted time series, while NEWS\_SENTIMENT provides current and historical market news and sentiment across equities, crypto, forex, and macro topics. The implementation should preserve the raw provider payload and provenance rather than only retaining a derived score.

Recommended Alpha Vantage input families for V1 are: adjusted daily price/volume history; company overview and relevant fundamentals for equities; NEWS\_SENTIMENT for asset-specific and macro evidence; and selected technical indicator endpoints only where they are useful as an independent cross-check. Many technical indicators can also be calculated locally from the adjusted price series, which improves reproducibility.

Every evidence object must include provider, endpoint/function, symbol or topic, provider timestamp, ingestion timestamp, and an immutable hash of the raw payload. This permits a recommendation to be reconstructed later.

# **4\. Stage 3: deterministic quantitative feature engine**

Before invoking an LLM, calculate everything that can be calculated deterministically. This avoids wasting model context and prevents the language model from performing arithmetic that a numerical library can perform exactly.

## **4.1 Returns**

For adjusted closing price P\_i,t, use log return as the canonical historical return series:

r\_i,t \= ln(P\_i,t / P\_i,t-1)

Log returns are additive across time and convenient for statistical modeling. For user-facing performance, the system may still report simple percentage returns.

## **4.2 Momentum**

A simple k-period momentum feature can be defined as cumulative log return:

M\_i,t^(k) \= ln(P\_i,t / P\_i,t-k)

V1 should compute multiple horizons rather than relying on one lookback, for example 5, 20, 60, and 252 trading days. Each feature is standardized cross-sectionally or relative to its own historical distribution before entering the ensemble.

## **4.3 Volatility and drawdown**

For a window of L observations, sample volatility is:

sigma\_i,t \= sqrt(252) \* std(r\_i,t-L+1, ..., r\_i,t)

The annualization factor should be parameterized by asset class. Crypto trades continuously and should not blindly inherit the 252-day convention if the return sampling convention differs.

Maximum drawdown over a lookback window is computed from the running peak of portfolio or asset value:

DD\_t \= P\_t / max\_{s \<= t}(P\_s) \- 1

MDD \= min\_t(DD\_t)

## **4.4 Standardization**

Signals from different sources have different scales. A raw 14-day RSI value, a 60-day return, and a sentiment probability cannot be combined directly. Quantitative features should first be transformed into normalized scores. A standard z-score is:

z\_i,t \= (x\_i,t \- mean(x\_i)) / std(x\_i)

Use rolling or expanding historical estimates that only use information available at time t. Never normalize using future observations, because that introduces look-ahead bias.

# **5\. Stage 4: LLM semantic signal extraction**

The LLM is used only where the input contains meaning that is difficult to reduce to deterministic numerical features. Typical examples include earnings commentary, product announcements, regulatory news, acquisition narratives, litigation headlines, management guidance, and the relationship between a news article and a particular asset.

The output is not BUY, SELL, or a target portfolio weight. The output is a structured semantic observation.

| Field | Domain | Meaning |
| :---- | :---- | :---- |
| direction | Real number in \[-1, 1\] | \-1 strongly negative, 0 neutral, \+1 strongly positive |
| magnitude | Real number in \[0, 1\] | Estimated economic importance if the interpretation is correct |
| confidence | Real number in \[0, 1\] | Confidence in the extraction/interpretation, not forecast certainty |
| horizon\_days | Positive integer | Expected horizon over which evidence is relevant |
| novelty | Real number in \[0, 1\] | Whether the evidence adds information not already represented |
| evidence\_ids | Array | Immutable references to source evidence |
| rationale | Text | Short explanation constrained to supplied evidence |

Define an event-level semantic score e\_j for evidence item j as:

e\_j \= direction\_j \* magnitude\_j \* confidence\_j \* novelty\_j

This is intentionally bounded to \[-1, 1\]. It is not interpreted as an expected return. It is only a semantic feature. Multiple events for asset i can be aggregated with time decay:

S\_i,t^LLM \= \[sum\_j exp(-lambda \* age\_j) \* e\_j\] / \[sum\_j exp(-lambda \* age\_j)\]

where age\_j is the age of event j in days and lambda controls decay. Horizon-specific semantic states should be maintained separately so that a one-day event is not mixed indiscriminately with a multi-quarter thesis.

The LLM must receive evidence with timestamps and must return a strict schema. The prompt must explicitly prohibit portfolio allocation, price targets without source support, and facts not present in the supplied evidence. A second validation step should reject malformed or unsupported outputs.

# **6\. Stage 5-6: signal calibration and expected-return estimation**

This is the most important mathematical transition. A sentiment score is not an expected return. Momentum is not an expected return. The engine needs a calibrated mapping from observable signals to a forecast distribution.

Let f\_i,t be the feature vector for asset i at time t. It can include quantitative and LLM-derived values:

f\_i,t \= \[momentum\_5, momentum\_20, volatility, drawdown, volume\_z, technical\_z, LLM\_semantic, fundamental\_z, ...\]^T

For a chosen forecast horizon H, define future realized return as:

y\_i,t^(H) \= ln(P\_i,t+H / P\_i,t)

The forecasting model estimates:

mu\_i,t^(H) \= E\[y\_i,t^(H) | f\_i,t\]

For V1, prefer a regularized linear or robust regression baseline before introducing complex neural forecasting. A transparent baseline can be written as:

mu\_i,t \= beta\_0 \+ beta^T f\_i,t

The coefficients beta must be learned on historical data using walk-forward or expanding-window training. This is crucial. Do not manually assume that a \+0.8 LLM sentiment score corresponds to \+8% expected return.

## **6.1 Confidence and forecast shrinkage**

Raw forecasts should be shrunk toward a conservative prior because financial return prediction is noisy. Let mu\_raw be the model forecast and mu\_prior a baseline forecast, often zero, a market-implied estimate, or a long-run asset-class prior. Define forecast confidence c\_i in \[0,1\]. Then:

mu\_i \= c\_i \* mu\_i^raw \+ (1 \- c\_i) \* mu\_i^prior

Confidence should be calibrated from historical forecast performance, evidence coverage, feature stability, and model uncertainty. It should not simply equal the LLM confidence field.

## **6.2 Ensemble formulation**

If multiple independent forecasting models are used, combine them by learned weights rather than arbitrary prompt logic. For K models:

mu\_i \= sum\_{k=1..K} alpha\_k \* mu\_i,k,     alpha\_k \>= 0,     sum\_k alpha\_k \= 1

Weights alpha\_k should be estimated from out-of-sample historical accuracy, optionally conditioned on market regime. The system should retain each component forecast so the recommendation remains explainable.

# **7\. Stage 7: portfolio risk model**

Expected returns alone are insufficient because assets move together. If two assets are highly correlated, adding both can create substantially more portfolio risk than evaluating each independently would suggest.

Let r\_t be the n-dimensional vector of asset returns. The covariance matrix is:

Sigma \= E\[(r \- mu\_r)(r \- mu\_r)^T\]

For portfolio weight vector w, portfolio variance is:

sigma\_p^2 \= w^T Sigma w

This quadratic term is why classical mean-variance portfolio optimization is a quadratic optimization problem rather than ordinary linear programming.

A raw sample covariance matrix becomes unstable when the asset universe is large relative to available observations. V1 should support covariance shrinkage, preferably Ledoit-Wolf or another shrinkage estimator, and optionally exponentially weighted covariance for more responsiveness. The chosen estimator must be backtested.

Risk estimation must be independent of the LLM. The LLM may describe risks in language, but numerical covariance, volatility, beta, and drawdown calculations come from statistical code.

# **8\. Stage 8: continuous target portfolio optimization**

The first optimizer computes an ideal continuous portfolio. Use CVXPY in Python as the modeling layer because the canonical objective is convex when Sigma is positive semidefinite and constraints are affine. CVXPY directly expresses quadratic programs and can route them to an installed numerical solver.

Let n be the number of investable assets. Define decision variable:

w in R^n, where w\_i is the post-rebalance portfolio weight of asset i

Define expected-return vector mu in R^n and covariance matrix Sigma in R^(n x n). A basic risk-adjusted objective is:

maximize    mu^T w \- lambda \* w^T Sigma w

where lambda \> 0 is the investor risk-aversion coefficient. Higher lambda penalizes variance more strongly.

A more practical objective includes turnover relative to current weight w0:

maximize    mu^T w \- lambda \* w^T Sigma w \- gamma \* ||w \- w0||\_1

where gamma \>= 0 is the turnover penalty. The L1 term discourages unnecessary trading and creates sparse adjustments.

## **8.1 Core constraints**

The optimizer should express investment policy as explicit mathematical constraints. Examples follow.

sum\_i w\_i \= 1

Full capital accounting, including cash as an asset if cash must be explicitly represented.

l\_i \<= w\_i \<= u\_i

Per-asset lower and upper bounds. For a long-only portfolio, l\_i \= 0\.

sum\_{i in sector k} w\_i \<= U\_k

Sector or asset-class concentration limit.

w\_cash \>= C\_min

Minimum cash reserve.

||w \- w0||\_1 \<= T\_max

Hard maximum turnover, if desired in addition to the turnover penalty.

The solver output must include solution status, objective value, target weights, expected portfolio return mu^T w, predicted variance w^T Sigma w, and dual values where supported. Dual values are useful because they reveal which constraints are binding and therefore explain why an apparently attractive asset was not allocated more capital.

# **9\. Stage 9: executable rebalance with mixed-integer optimization**

The continuous optimizer may return economically sensible target weights that are operationally awkward, such as a $17 trade, too many small trades, or fractional quantities that cannot be executed under a specific rule. A second optimization stage converts the target into executable trades.

For each asset i, define buy amount b\_i \>= 0 and sell amount s\_i \>= 0\. If h\_i is current dollar holding, then post-trade holding is:

h\_i^new \= h\_i \+ b\_i \- s\_i

Introduce binary trade variable z\_i in {0,1}:

z\_i \= 1 if any trade is executed in asset i; otherwise 0

Minimum trade size m\_i can be modeled using a sufficiently large upper bound M\_i:

m\_i \* z\_i \<= b\_i \+ s\_i \<= M\_i \* z\_i

A maximum number of touched positions becomes:

sum\_i z\_i \<= K\_max

This is a mixed-integer optimization problem because it contains continuous dollar variables and binary decisions.

Tool choice: use a MIP-capable solver. OR-Tools MPSolver can model LP/MIP and can use supported backends such as SCIP where available. Google OR-Tools CP-SAT is excellent for pure integer and logical problems, but CP-SAT requires integer coefficients and variables, so monetary quantities must be scaled to cents or another integer unit. For this portfolio problem, CVXPY should remain the default for the continuous quadratic target portfolio. Use OR-Tools or another MIP-capable solver only for the discrete execution layer when its capabilities are actually needed.

Do not force the entire portfolio optimization into CP-SAT simply because the application uses constraints. The portfolio risk objective w^T Sigma w is naturally quadratic and continuous. Choosing the solver should follow the mathematical structure of the problem.

# **10\. Transaction costs, slippage, and trade penalties**

The optimizer should optimize net expected benefit, not theoretical gross return. Let q\_i denote signed trade amount and c\_i(q\_i) estimated execution cost. A practical objective is:

maximize    mu^T w \- lambda \* w^T Sigma w \- sum\_i c\_i(q\_i)

For a simple V1 approximation, use proportional cost:

c\_i(q\_i) \= k\_i \* |q\_i|

where k\_i includes estimated spread, fees, and slippage. For illiquid assets, a nonlinear market-impact model may later replace this approximation. Alpaca commission policy does not eliminate spread and market-impact costs, so zero broker commission must not be interpreted as zero transaction cost.

# **11\. Stage 10: stress testing and scenario validation**

A single optimal solution is not sufficient. The system should ask whether the recommendation remains reasonable when its assumptions are perturbed.

At minimum, test forecast shrinkage, covariance perturbations, market drawdowns, volatility spikes, and reduced liquidity. Let scenario s have return vector R\_s and probability p\_s. Scenario portfolio return is:

R\_p,s \= w^T R\_s

Expected scenario return is:

E\[R\_p\] \= sum\_s p\_s \* R\_p,s

A future risk objective can incorporate Conditional Value at Risk (CVaR). For loss L and confidence alpha, VaR\_alpha is the alpha-quantile of loss, while CVaR\_alpha is the expected loss in the tail beyond VaR. This is useful when downside tails matter more than symmetric variance.

A recommendation must be rejected or flagged if small changes in mu or Sigma produce radically different allocations. That behavior is evidence of optimizer instability or weak signal strength.

# **12\. Stage 11: LLM explanation after optimization**

Only after the mathematical solution is finalized does the LLM receive the proposed rebalance. Its job is explanatory, not decisional.

The explanation context should contain current weights, target weights, proposed trades, expected-return contributions, risk contributions, binding constraints, signal provenance, forecast confidence, and stress-test results. The LLM should explain why the optimizer increased, reduced, retained, or excluded positions.

Example: if NVDA has a strong positive forecast but the optimizer reduces it, the explanation should be grounded in mathematical facts such as concentration, covariance, turnover, or risk constraints rather than inventing a bearish narrative.

The explanation validator should compare every numerical statement against optimizer output. Any unsupported claim should cause regeneration or removal.

# **13\. Stage 12: Alpaca execution boundary**

Broker execution is intentionally downstream of signal creation and optimization. The optimizer outputs a proposed trade plan. A policy engine decides whether the recommendation requires manual approval, can be auto-executed within user-defined limits, or must be rejected.

Immediately before order placement, refresh account buying power and current positions from Alpaca, validate market status and asset tradability, check that prices have not moved beyond configured slippage tolerance, and recompute order quantities. If portfolio state materially changed since optimization, invalidate the plan and rerun the cycle rather than patching it with an LLM.

Persist broker order IDs, intended quantities, actual fills, average fill price, and timestamps. The next recommendation cycle uses actual fills, not assumed fills.

# **14\. Formal system definition**

At decision time t, define the information set I\_t as all admissible data known at or before t. This includes market data, provider intelligence, portfolio state, and policy constraints. No training, feature, or decision may use information outside I\_t.

I\_t \= {market history \<= t, evidence \<= t, holdings\_t, policy\_t}

For each asset i, the feature engine computes f\_i,t \= F(I\_t). The forecasting model produces expected H-period return and forecast uncertainty:

(mu\_i,t, tau\_i,t) \= G(f\_i,t)

The risk model estimates covariance using information available at t:

Sigma\_t \= R(I\_t)

The continuous optimizer solves:

w\*\_t \= argmax\_w \[mu\_t^T w \- lambda w^T Sigma\_t w \- gamma ||w \- w0\_t||\_1\]

subject to portfolio policy constraints A w \<= b and E w \= d, plus lower and upper bounds. The executable rebalance optimizer then finds trade vector q\_t that approximates w\*\_t while satisfying discrete execution rules and minimizing implementation cost.

q\*\_t \= argmin\_q \[tracking\_error(w(q), w\*\_t) \+ execution\_cost(q)\]

The LLM receives the resulting mathematical state M\_t and produces explanation text X\_t:

X\_t \= LLM(M\_t, evidence\_provenance\_t)

Critically, there is no function in which the final order vector is directly emitted by the LLM.

# **15\. Mathematical and implementation tool stack**

| Tool | Role | Specific use |
| :---- | :---- | :---- |
| NumPy | Vector/matrix arithmetic | Returns, weights, covariance inputs, linear algebra. |
| pandas or Polars | Time-series tabular transformations | Alignment, rolling windows, feature construction, timestamps. |
| SciPy | Statistics and numerical routines | Distribution functions, optimization utilities, statistical tests. |
| scikit-learn | Forecast calibration and baseline ML | Regularized regression, covariance shrinkage such as Ledoit-Wolf, calibration, pipelines. |
| CVXPY | Primary continuous optimization modeling layer | Mean-variance quadratic program, turnover penalties, affine constraints, CVaR formulations. |
| Numerical QP/conic solver used through CVXPY | Solve convex program | Select an installed solver appropriate to the formulation; record solver/version/status. |
| Google OR-Tools MPSolver | Optional MIP execution layer | Binary trade/no-trade decisions, lot rules, bounded number of trades, discrete operational constraints. |
| Google OR-Tools CP-SAT | Optional integer/logical layer | Use when the execution problem is naturally integer/logical. Scale monetary quantities to integer units. |
| Alpha Vantage | Market evidence provider | Adjusted time series, fundamentals, technical indicators, news/sentiment intelligence. |
| LLM with strict structured output | Semantic extraction and explanation | Interpret unstructured evidence into bounded features; explain finalized optimizer output. |
| Alpaca API | Execution only | Positions/account refresh, order placement, order/fill monitoring. Never acts as signal generator. |

# **16\. Required data contracts between stages**

Every stage must produce a typed artifact. This prevents an implementation LLM from collapsing the architecture into a single prompt.

| Contract | Minimum contents |
| :---- | :---- |
| EvidenceRecord | raw payload reference, source, timestamp, symbol/topic, hash |
| QuantFeatureVector | asset\_id, as\_of, feature\_name/value pairs, lookback metadata |
| SemanticSignal | asset\_id, evidence\_ids, direction, magnitude, confidence, novelty, horizon, rationale |
| ForecastRecord | asset\_id, horizon, mu\_raw, mu\_shrunk, forecast\_std, model\_version, calibration metadata |
| RiskModel | asset universe, as\_of, Sigma, estimator, lookback, PSD validation status |
| OptimizationProblem | mu, Sigma, w0, constraints, lambda, gamma, solver configuration |
| OptimizationResult | status, target weights, objective, expected return, variance, binding constraints, duals |
| RebalancePlan | asset\_id, side, quantity/notional, expected cost, target delta, reason codes |
| StressTestReport | scenario results, sensitivity results, instability flags |
| ExecutionRecord | broker order ID, submitted order, fill, price, timestamp, status |

# **17\. Evaluation and backtesting requirements**

The system should not be considered successful because its recommendations sound persuasive. Every signal layer must be evaluated against future observations using time-respecting backtests.

Use walk-forward evaluation. At each historical decision date t, train or calibrate only on data available before t, create the recommendation as if the system were live, and then measure realized future performance.

Signal quality metrics should include directional accuracy, rank information coefficient, calibration error, forecast mean squared error, and stability by regime. Portfolio metrics should include annualized return, volatility, Sharpe ratio, Sortino ratio, maximum drawdown, turnover, estimated transaction cost, concentration, and benchmark-relative performance.

Ablation tests are mandatory. Compare: quantitative-only; LLM-semantic-only; combined ensemble; no turnover penalty; alternate covariance estimator; and naive LLM-direct recommendation. The purpose is to determine whether each component adds measurable value rather than assuming it does.

Any use of news must model publication timestamps carefully. Historical backtests must use the timestamp at which the article or provider signal became available, not the date of an event described inside the article.

# **18\. Safety, correctness, and failure modes**

The engine must prefer refusing to produce an optimized trade plan over solving with corrupted or insufficient inputs.

| Failure | Required behavior |
| :---- | :---- |
| Stale portfolio state | Invalidate recommendation; refresh Alpaca positions/account state. |
| Missing/insufficient price history | Exclude asset or use explicitly defined fallback; never hallucinate volatility. |
| Covariance matrix not positive semidefinite | Apply documented PSD repair/shrinkage; log the repair. |
| Optimizer infeasible | Return INFEASIBLE with conflicting policy diagnostics; do not ask LLM to improvise. |
| Optimizer unbounded/model invalid | Treat as engineering failure; no trade output. |
| LLM semantic schema invalid | Retry structured extraction once under policy, otherwise discard that semantic signal. |
| Evidence conflict | Retain competing evidence and lower calibrated confidence; do not let the LLM silently choose one. |
| Extreme concentration caused by forecast error | Enforce hard asset/sector caps independent of forecast. |
| Rapid price move after solve | Invalidate plan if move exceeds configured tolerance and recompute. |
| No statistically meaningful edge | Prefer current allocation/cash-preserving solution over forced trading. |

# **19\. Recommended V1 implementation sequence**

Do not implement every mathematical technique at once. V1 should establish a rigorous baseline that can later be beaten experimentally.

1. Build a historical Alpha Vantage ingestion layer and immutable evidence store. Preserve adjusted prices and exact evidence timestamps.  
2. Build deterministic features from adjusted prices and volume. Start with returns, momentum horizons, realized volatility, drawdown, volume anomaly, and a small set of technical features.  
3. Add structured LLM semantic extraction from NEWS\_SENTIMENT evidence. Keep the output bounded and strictly evidence-grounded.  
4. Create a walk-forward supervised calibration dataset in which features at t predict realized return over predefined horizons H.  
5. Train a transparent baseline expected-return model, such as ridge/elastic-net regression, and calibrate/shrink forecasts.  
6. Estimate covariance with a shrinkage estimator and compare it with exponentially weighted covariance.  
7. Implement continuous long-only portfolio optimization in CVXPY with asset caps, asset-class caps, cash floor, and turnover penalty.  
8. Add stress tests and optimizer sensitivity diagnostics before any broker execution.  
9. Add the LLM explanation layer using only finalized mathematical outputs and evidence provenance.  
10. Integrate Alpaca in paper-trading mode first. Measure recommendation quality and execution drift.  
11. Only after the continuous optimizer is stable, add OR-Tools/MIP for minimum notional, maximum trade count, lot/discrete rules, and more complex execution policy.

This order matters. Adding mixed-integer complexity before demonstrating that the forecast and continuous portfolio objective have value will make the system harder to debug without improving the underlying signal.

# **20\. Worked conceptual example**

Assume the investable universe is AAPL, NVDA, BTC, TLT, and cash. At time t, the engine ingests the current portfolio and market evidence. Quantitative features detect positive medium-term momentum in NVDA but elevated volatility and strong correlation with existing technology exposure. Alpha Vantage news contains positive earnings evidence. The LLM converts the textual evidence into a positive semantic feature with bounded magnitude and confidence.

The calibrated forecasting model combines these features and estimates expected H-period return mu\_NVDA. Separately, the covariance model estimates how NVDA contributes to portfolio risk. The optimizer therefore sees both the positive expected return and its interaction with the rest of the portfolio.

If the portfolio is already technology-heavy, the sector constraint or variance penalty can prevent additional NVDA concentration. The correct explanation is then not “NVDA is unattractive.” It is: “NVDA remains positively forecast, but additional exposure has lower portfolio-level utility because of correlated technology risk and concentration constraints.”

This example illustrates the core principle of the architecture: an asset-level signal and a portfolio-level decision are different objects.

# **21\. V1 acceptance criteria**

| Area | Acceptance criterion |
| :---- | :---- |
| Chronology | No recommendation path can bypass evidence normalization, forecast/risk generation, and optimizer validation. |
| Determinism | Given identical model versions, evidence, state, and solver settings, deterministic stages reproduce identical outputs within numerical tolerance. |
| Auditability | Every semantic signal and recommendation traces back to evidence IDs and model/solver versions. |
| Constraint guarantee | No recommendation violates hard user policy when optimizer status is accepted. |
| No LLM allocation | Final portfolio weights and trade notionals are never generated directly by the LLM. |
| Backtesting | Walk-forward evaluation exists with strict timestamp controls and no look-ahead leakage. |
| Ablation | The team can measure incremental contribution from LLM semantic signals versus quantitative-only baseline. |
| Execution isolation | Alpaca order placement is permissioned and can be disabled while all upstream recommendations continue functioning. |

# **22\. Technical references used for this PRD**

Alpha Vantage API Documentation. Current documentation describes Alpha Intelligence NEWS\_SENTIMENT, adjusted time-series data, fundamentals, and technical indicator APIs. https://www.alphavantage.co/documentation/

CVXPY Quadratic Program Example and API Reference. CVXPY documents standard convex quadratic programs and explicitly uses portfolio allocation as an example. https://www.cvxpy.org/examples/basic/quadratic\_program.html

Google OR-Tools Constraint Optimization, CP-SAT, and MPSolver documentation. OR-Tools documents CP-SAT as an integer solver and MPSolver as the LP/MIP interface. https://developers.google.com/optimization/

Implementation note: library and solver versions must be pinned in the project lockfile and persisted with each optimization result. Solver behavior and availability can change across environments.