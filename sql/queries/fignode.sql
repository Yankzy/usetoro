-- =========================================================================
-- Auth: Employee Registration & Login
-- =========================================================================

-- name: CreateEmployeeUser :one
INSERT INTO toro_core.users (entity_id, email, password_hash, role, user_type)
VALUES ($1, $2, $3, 'member', 'employee') RETURNING id;

-- name: CreateEmployeeProfile :exec
INSERT INTO fignode.employee_profiles (user_id, first_name, last_name, is_manager)
VALUES ($1, $2, $3, $4);

-- name: GetEmployeeByEmail :one
SELECT
    u.id,
    u.email,
    u.password_hash,
    u.entity_id,
    u.is_active,
    p.first_name,
    p.last_name,
    p.is_manager,
    p.ai_accuracy_score,
    p.streak,
    p.total_cleared,
    p.today_cleared,
    p.created_at
FROM toro_core.users u
JOIN fignode.employee_profiles p ON p.user_id = u.id
WHERE u.email = $1 AND u.user_type = 'employee';

-- name: GetEmployeeByID :one
SELECT
    u.id,
    u.email,
    u.entity_id,
    u.is_active,
    p.first_name,
    p.last_name,
    p.is_manager,
    p.ai_accuracy_score,
    p.streak,
    p.total_cleared,
    p.today_cleared,
    p.created_at
FROM toro_core.users u
JOIN fignode.employee_profiles p ON p.user_id = u.id
WHERE u.id = $1 AND u.user_type = 'employee';

-- name: GetEmployeePublic :one
SELECT
    u.id,
    u.email,
    p.first_name,
    p.last_name,
    p.is_manager,
    p.ai_accuracy_score,
    p.streak,
    p.total_cleared,
    p.today_cleared,
    p.created_at
FROM toro_core.users u
JOIN fignode.employee_profiles p ON p.user_id = u.id
WHERE u.id = $1 AND u.user_type = 'employee';

-- =========================================================================
-- Status Updates (Legacy endpoints mapped to new schema)
-- =========================================================================

-- name: ClearTransaction :exec
UPDATE fignode.staging_transactions
SET status = 'APPROVED', updated_at = now()
WHERE id = $1;

-- name: SetTransactionInReview :exec
UPDATE fignode.staging_transactions
SET status = 'PENDING', updated_at = now()
WHERE id = $1;

-- name: IncrementEmployeeCleared :exec
UPDATE fignode.employee_profiles
SET total_cleared = total_cleared + 1,
    today_cleared = CASE
        WHEN today_cleared_date = CURRENT_DATE THEN today_cleared + 1
        ELSE 1
    END,
    today_cleared_date = CURRENT_DATE,
    updated_at = now()
WHERE user_id = $1;

-- =========================================================================
-- Stats: Employee dashboard
-- =========================================================================

-- name: GetEmployeeStats :one
SELECT
    p.total_cleared,
    p.today_cleared,
    p.streak,
    p.ai_accuracy_score
FROM fignode.employee_profiles p
WHERE p.user_id = $1;

-- =========================================================================
-- Leaderboard: Snapshot computation & retrieval
-- =========================================================================

-- name: ComputeAllTimeLeaderboard :many
SELECT
    p.user_id,
    u.email,
    p.total_cleared AS cleared,
    p.streak
FROM fignode.employee_profiles p
JOIN toro_core.users u ON u.id = p.user_id
WHERE u.is_active = TRUE
ORDER BY p.total_cleared DESC,
         p.streak DESC,
         u.email ASC
LIMIT 100;

-- name: ComputePeriodLeaderboard :many
SELECT
    p.user_id,
    u.email,
    COUNT(t.id)::INT AS cleared,
    p.streak
FROM fignode.employee_profiles p
JOIN toro_core.users u ON u.id = p.user_id
LEFT JOIN fignode.staging_transactions t
    ON t.swiped_by = p.user_id 
    AND t.status IN ('APPROVED', 'POSTED')
    AND t.updated_at >= @since::TIMESTAMPTZ
WHERE u.is_active = TRUE
GROUP BY p.user_id, u.email, p.streak
ORDER BY COUNT(t.id) DESC,
         p.streak DESC,
         u.email ASC
LIMIT 100;

-- name: InsertLeaderboardSnapshot :exec
INSERT INTO fignode.leaderboard_snapshots (period, entries)
VALUES ($1, $2);

-- name: GetLatestLeaderboardSnapshot :one
SELECT entries, computed_at
FROM fignode.leaderboard_snapshots
WHERE period = $1
ORDER BY computed_at DESC
LIMIT 1;

-- =========================================================================
-- Streak: Update & midnight reset
-- =========================================================================

-- name: UpdateStreak :exec
UPDATE fignode.employee_profiles
SET streak = CASE
        WHEN streak_last_date IS NULL OR streak_last_date < CURRENT_DATE - INTERVAL '1 day'
            THEN 1
        WHEN streak_last_date = CURRENT_DATE - INTERVAL '1 day'
            THEN streak + 1
        ELSE streak
    END,
    streak_last_date = CURRENT_DATE,
    updated_at = now()
WHERE user_id = $1 AND (streak_last_date IS NULL OR streak_last_date < CURRENT_DATE);

-- name: ResetStaleStreaks :exec
UPDATE fignode.employee_profiles
SET streak = 0, updated_at = now()
WHERE streak > 0
  AND (streak_last_date IS NULL OR streak_last_date < CURRENT_DATE - INTERVAL '1 day');

-- name: ResetTodayCleared :exec
UPDATE fignode.employee_profiles
SET today_cleared = 0, today_cleared_date = CURRENT_DATE, updated_at = now()
WHERE today_cleared_date IS NULL OR today_cleared_date < CURRENT_DATE;

-- =========================================================================
-- Badges: Award & query (schema removed)
-- =========================================================================

-- =========================================================================
-- Skip: Record a skip
-- =========================================================================

-- name: InsertSkip :exec
UPDATE fignode.staging_transactions
SET status = 'SKIPPED', updated_at = now()
WHERE id = $1;

-- =========================================================================
-- Rule evaluation worker
-- =========================================================================

-- name: GetPendingStagingTransactions :many
SELECT * FROM fignode.staging_transactions
WHERE session_id = $1 AND status = 'ENRICHED';

-- name: UpdateStagingTransactionWithRule :exec
UPDATE fignode.staging_transactions
SET rule_group_id = sqlc.narg('rule_group_id'),
    predicted_account_id = sqlc.narg('predicted_account_id'),
    predicted_vendor_id = sqlc.narg('predicted_vendor_id'),
    predicted_customer_id = sqlc.narg('predicted_customer_id'),
    status = sqlc.narg('status'),
    ai_reasoning = sqlc.narg('ai_reasoning'),
    cash_direction = sqlc.narg('cash_direction'),
    predicted_account_name = sqlc.narg('predicted_account_name'),
    predicted_vendor_name = sqlc.narg('predicted_vendor_name'),
    predicted_customer_name = sqlc.narg('predicted_customer_name'),
    confidence_score = sqlc.narg('confidence_score'),
    merchant_name = sqlc.narg('merchant_name'),
    updated_at = NOW()
WHERE id = sqlc.narg('id');

-- name: UpdateStagingTransactionCashDirection :exec
UPDATE fignode.staging_transactions
SET cash_direction = $2, updated_at = NOW()
WHERE id = $1;

-- name: UpdateStagingTransactionMacroClass :exec
UPDATE fignode.staging_transactions
SET macro_class = $2, ai_reasoning = $3, updated_at = NOW()
WHERE id = $1;

-- name: UpdateStagingTransactionAccountType :exec
UPDATE fignode.staging_transactions
SET account_type = $2, updated_at = NOW()
WHERE id = $1;

-- name: GetUnmatchedSessionRows :many
SELECT * FROM fignode.staging_transactions
WHERE session_id = $1 AND rule_group_id IS NULL AND status = 'ENRICHED';

-- name: GetUnmatchedRowsByMacroClass :many
SELECT * FROM fignode.staging_transactions
WHERE session_id = $1 AND rule_group_id IS NULL AND macro_class = $2;

-- name: GetDistinctMacroClassesUnmatched :many
SELECT DISTINCT macro_class FROM fignode.staging_transactions
WHERE session_id = $1 AND rule_group_id IS NULL AND macro_class IS NOT NULL;


-- =========================================================================
-- Updates
-- =========================================================================

-- name: GetEmployeeProfileForUpdate :one
SELECT * FROM fignode.employee_profiles
WHERE user_id = $1
FOR UPDATE;

-- name: AdjustAccuracyScore :exec
UPDATE fignode.employee_profiles
SET ai_accuracy_score = CASE
        WHEN @delta::NUMERIC > 0 THEN LEAST(1.000, ai_accuracy_score + @delta)
        ELSE GREATEST(0.000, ai_accuracy_score + @delta)
    END,
    updated_at = now()
WHERE user_id = @user_id;

-- =========================================================================
-- Transactions Batch
-- =========================================================================

-- name: GetPendingFignodeTransactions :many
SELECT * FROM fignode.staging_transactions
WHERE status = 'PENDING_AI' 
  AND human_action IS NULL
  AND session_id IS NOT NULL -- Example: filter logic
ORDER BY created_at DESC
LIMIT 10;

-- =========================================================================
-- Fignode Transactions Startup
-- =========================================================================

-- name: GetInitialEnrichedTransactionsByRealm :many
SELECT cs.* FROM fignode.staging_transactions cs
JOIN fignode.staging_sessions ss ON ss.id = cs.session_id
WHERE cs.status = 'READY_FOR_REVIEW' AND ss.realm_id = $1 AND cs.duplicate_of IS NULL
ORDER BY cs.created_at DESC LIMIT 50;

-- name: CountEnrichedTransactionsBySession :one
SELECT COUNT(*) FROM fignode.staging_transactions
WHERE session_id = $1 AND status = 'ENRICHED';

-- name: UpdateSessionTransactionsToReadyForReview :exec
UPDATE fignode.staging_transactions
SET status = 'READY_FOR_REVIEW'
WHERE session_id = $1 AND status = 'ENRICHED';
